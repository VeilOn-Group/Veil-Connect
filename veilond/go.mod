module github.com/veilon/veilon-app/veilond

go 1.24.0

toolchain go1.24.7

require (
	github.com/goccy/go-yaml v1.18.0
	github.com/veilon/veil-shared v0.0.0
	golang.org/x/sys v0.40.0
)

require github.com/gowebpki/jcs v1.0.2 // indirect

replace github.com/veilon/veil-shared => ../../veil-shared
