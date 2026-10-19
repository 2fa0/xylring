module gitclient

go 1.27.2

tool github.com/egoist/mygo/cmd/mygo

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

require (
	github.com/egoist/mygo v0.5.5
	github.com/egoist/mygo/gitclient-editor v0.0.0
)

replace github.com/egoist/mygo/gitclient-editor => ../third_party/mygo-editor
