// corebind is the single gomobile bind unit for the Android app: sing-box
// libbox and veil-shared/mobile are bound together into ONE AAR
// (veilon-core.aar, native library libveilcore.so). Two separate gomobile
// AARs would each ship their own Go runtime and the same go.Seq Java
// classes, which collide at dex/link time.
module github.com/veilon/veilon-app/android/corebind

go 1.24.7

require (
	github.com/sagernet/gomobile v0.1.12
	github.com/sagernet/sing-box v1.13.21
	github.com/veilon/veil-shared v0.0.0
)

replace github.com/veilon/veil-shared => ../../../veil-shared
