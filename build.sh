APP=clink
ENTRY=.
CGO_ENABLED=0
mkdir -p dist
for GOOS in linux darwin windows; do
  for GOARCH in amd64 arm64; do
    EXT=""
    [ "$GOOS" = "windows" ] && EXT=".exe"
    OUT="dist/${APP}-${GOOS}-${GOARCH}${EXT}"
    GOOS=$GOOS GOARCH=$GOARCH CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$OUT" "$ENTRY"
  done
done
