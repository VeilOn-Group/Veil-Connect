module github.com/veilon/veilon-app/desktop

// Wails v3 v3.0.0-beta.34 requires Go >= 1.26 (verified: `go get` refuses on 1.24).
go 1.26

require (
	github.com/veilon/veilon-app/veilond v0.0.0
	github.com/wailsapp/wails/v3 v3.0.0-beta.34
)

replace (
	github.com/veilon/veilon-app/veilond => ../veilond
	github.com/veilon/veil-shared => ../../veil-shared
)
