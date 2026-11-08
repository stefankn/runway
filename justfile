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
    rm -rf bin dist

# Check the release config and build a snapshot into dist/ (publishes nothing)
release-check:
    goreleaser check
    goreleaser release --snapshot --clean

# Tag a release and push the tag, which starts the release workflow, e.g. `just release 0.1.0`
release version:
    #!/usr/bin/env bash
    set -euo pipefail
    die() { echo "release: $*" >&2; exit 1; }

    v="${1#v}"
    [[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || die "invalid version '$1': use X.Y.Z, e.g. 0.1.0"
    tag="v$v"

    branch=$(git rev-parse --abbrev-ref HEAD)
    [[ "$branch" == main ]] || die "on branch '$branch'; releases are tagged from main"
    [[ -z "$(git status --porcelain)" ]] || die "there are uncommitted changes; commit or stash them first"
    git rev-parse -q --verify "refs/tags/$tag" >/dev/null && die "tag $tag already exists"
    git fetch --quiet origin main
    [[ "$(git rev-parse HEAD)" == "$(git rev-parse origin/main)" ]] || die "main differs from origin/main; push or pull first"
    [[ -z "$(git ls-remote --tags origin "refs/tags/$tag")" ]] || die "tag $tag already exists on origin"

    just ci
    goreleaser check

    read -r -p "Tag $tag at $(git rev-parse --short HEAD) and push it to origin? [y/N] " answer
    [[ "$answer" == [yY] ]] || die "cancelled; no tag created"
    git tag -a "$tag" -m "runway $tag"
    git push origin "$tag"
    echo "Pushed $tag. Follow the release at https://github.com/stefankn/runway/actions"
