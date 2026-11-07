# runway — common development tasks. Run `just` to list recipes.

# Pass recipe arguments as "$@" so values with spaces (simulator names) stay intact.
set positional-arguments

staticcheck_version := "v0.8.2"

version := env("VERSION", "dev")
commit  := `git rev-parse --short HEAD 2>/dev/null || echo none`
date    := `date -u +%Y-%m-%dT%H:%M:%SZ`
ldflags := "-X main.version=" + version + " -X main.commit=" + commit + " -X main.date=" + date

# List available recipes
default:
    @just --list

# Build the binary into bin/runway (override version with VERSION=v0.1.0 just build)
build:
    go build -trimpath -ldflags "{{ldflags}}" -o bin/runway ./cmd/runway

# Run runway with arguments, e.g. `just run -v version`
run *args:
    go run -ldflags "{{ldflags}}" ./cmd/runway "$@"

# Run unit tests (pass extra flags, e.g. `just test -v -run Quote`)
test *flags:
    go test -race "$@" ./...

# Format all Go files
fmt:
    gofmt -w .

# Fail if any Go file is not formatted
fmt-check:
    @test -z "$(gofmt -l .)" || { echo "Not formatted:"; gofmt -l .; exit 1; }

# Run go vet and staticcheck
lint:
    go vet ./...
    go run honnef.co/go/tools/cmd/staticcheck@{{staticcheck_version}} ./...

# Tidy go.mod and go.sum
tidy:
    go mod tidy

# Run every check CI runs
ci:
    go mod tidy -diff
    @just fmt-check
    @just lint
    @just test

# Remove build output
clean:
    rm -rf bin
