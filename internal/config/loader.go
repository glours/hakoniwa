package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// defaultFileNames is the search order when no -f flag is given.
var defaultFileNames = []string{
	"hakoniwa.yaml",
	"hakoniwa.yml",
	"hako.yaml",
	"hako.yml",
	".sbxenv",
}

// SourcePos records the line and column of a YAML node (1-based).
type SourcePos struct {
	File   string
	Line   int
	Column int
}

func (p SourcePos) String() string {
	if p.Line == 0 {
		// Position was not found in the index; show file only.
		if p.File == "" {
			return ""
		}
		return p.File
	}
	if p.File == "" {
		return fmt.Sprintf("%d:%d", p.Line, p.Column)
	}
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Column)
}

// LoadResult bundles the parsed project with source-position metadata.
type LoadResult struct {
	Project  *Project
	Filename string
	// Positions maps dotted YAML paths (e.g. "agents.fixer.policy") to their
	// source positions in the file, for use in diagnostics.
	Positions map[string]SourcePos
}

// FindProjectFile searches the given directory (or the current working
// directory if dir is empty) for a project file using defaultFileNames, and
// returns the first match. Returns an error if no file is found.
func FindProjectFile(dir string) (string, error) {
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getwd: %w", err)
		}
	}
	for _, name := range defaultFileNames {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no project file found in %s (tried: %v)", dir, defaultFileNames)
}

// LoadReader parses a project file from the given io.Reader, using name for
// error messages and source positions.
func LoadReader(name string, r io.Reader) (*LoadResult, error) {
	return load(name, r)
}

// Load reads a project file from the given path, strictly decodes it, and
// returns the project together with a map of YAML-path → source position.
func Load(path string) (*LoadResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	return load(path, f)
}

// load is the internal loader that works on any io.Reader.
func load(name string, r io.Reader) (*LoadResult, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	// Decode into a raw yaml.Node to capture source positions.
	var rootNode yaml.Node
	if err := yaml.Unmarshal(data, &rootNode); err != nil {
		return nil, fmt.Errorf("parse %s: %w", name, err)
	}

	// Build the position index from the node tree.
	positions := make(map[string]SourcePos)
	if rootNode.Kind == yaml.DocumentNode && len(rootNode.Content) > 0 {
		indexNode(name, "", rootNode.Content[0], positions)
	}

	// Strict-decode into typed structs. gopkg.in/yaml.v3 does not expose a
	// KnownFields-equivalent on Unmarshal directly; we use a Decoder.
	dec := yaml.NewDecoder(newReader(data))
	dec.KnownFields(true)

	var project Project
	if err := dec.Decode(&project); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: file is empty", name)
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	// Reject multi-document files: a second Decode should reach EOF.
	var extra interface{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: multi-document YAML is not supported; use a single document", name)
	}

	return &LoadResult{
		Project:   &project,
		Filename:  name,
		Positions: positions,
	}, nil
}

// indexNode walks a yaml.Node tree and populates the positions map.
// path is the dotted path to the current node.
func indexNode(file, path string, node *yaml.Node, out map[string]SourcePos) {
	if path != "" {
		out[path] = SourcePos{File: file, Line: node.Line, Column: node.Column}
	}

	switch node.Kind {
	case yaml.MappingNode:
		// Mapping content is [key, value, key, value, …]
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			val := node.Content[i+1]
			childPath := key.Value
			if path != "" {
				childPath = path + "." + key.Value
			}
			indexNode(file, childPath, val, out)
		}
	case yaml.SequenceNode:
		for i, item := range node.Content {
			indexNode(file, fmt.Sprintf("%s[%d]", path, i), item, out)
		}
	}
}

// PosFor returns the source position for the given dotted path, or a zero
// SourcePos if the path was not found in the index.
func (r *LoadResult) PosFor(path string) SourcePos {
	if p, ok := r.Positions[path]; ok {
		return p
	}
	return SourcePos{File: r.Filename}
}
