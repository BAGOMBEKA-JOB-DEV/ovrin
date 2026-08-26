module github.com/BAGOMBEKA-JOB-DEV/ovrin/ocr/tesseract

go 1.22

require (
	github.com/BAGOMBEKA-JOB-DEV/ovrin v0.1.0
	github.com/danlock/gogosseract v0.0.11-0ad3421
)

require (
	github.com/danlock/pkg v0.0.17-a9828f2 // indirect
	github.com/jerbob92/wazero-emscripten-embind v1.3.0 // indirect
	github.com/tetratelabs/wazero v1.5.0 // indirect
	golang.org/x/exp v0.0.0-20231006140011-7918f672742d // indirect
	golang.org/x/text v0.13.0 // indirect
)

// The core module has no release tag yet, so the version above is a
// placeholder and this points it at the checkout beside us. A replace in a
// dependency's go.mod is ignored by whoever imports it, so this affects only
// builds of this module itself; it comes out when ovrin is tagged.
replace github.com/BAGOMBEKA-JOB-DEV/ovrin => ../..
