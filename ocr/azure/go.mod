module github.com/BAGOMBEKA-JOB-DEV/ovrin/ocr/azure

go 1.22

require github.com/BAGOMBEKA-JOB-DEV/ovrin v0.1.0

// The core module has no release tag yet, so the version above is a
// placeholder and this points it at the checkout beside us. A replace in a
// dependency's go.mod is ignored by whoever imports it, so this affects only
// builds of this module itself; it comes out when ovrin is tagged.
replace github.com/BAGOMBEKA-JOB-DEV/ovrin => ../..
