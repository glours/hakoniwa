# CI Workflow Setup

The GitHub Actions workflow file needs the `workflow` OAuth scope to push.
Copy the content below to `.github/workflows/ci.yml` in the repository.

```yaml
name: CI

on:
  push:
    branches: [ "**" ]
  pull_request:
    branches: [ "**" ]

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: "1.26.4"

      - name: Build
        run: go build ./...

      - name: Test
        run: go test ./...
```
