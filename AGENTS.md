# Agent Guidelines for clink

## Build & Test Commands
- Build: `go build -o clink .`
- Cross-compile: `./build.sh` (creates binaries in `dist/` for all platforms)
- Run client: `go run . -host localhost:9000`
- Run server: `go run . -server -host localhost:9000`
- Test: `go test ./...` (currently no tests)
- Format: `gofmt -w .`
- Lint: `go vet ./...`

## Code Style
- **Imports**: Standard library first, then third-party (blank line between), use named imports for clarity (e.g., `tea "github.com/charmbracelet/bubbletea"`)
- **Formatting**: Use `gofmt`, tabs for indentation
- **Types**: Explicit types, struct fields exported when needed for JSON/external use
- **Naming**: CamelCase for exports, camelCase for private, descriptive names (e.g., `connectedMsg`, `fetchMenuCmd`)
- **Error handling**: Check all errors explicitly, wrap with `fmt.Errorf("context: %w", err)` for context
- **Comments**: Minimal, only for public APIs or complex logic
- **Concurrency**: Use channels and goroutines for I/O operations (see `connectCmd`, `Hub.Run`)
