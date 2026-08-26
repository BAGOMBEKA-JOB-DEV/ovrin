// examples/receipt is its own module for the same reason every adapter is:
// it imports model/skyl, and a module's dependencies are inherited by everyone
// who imports it. Keeping the example in the root module would put skyl into
// the go.sum of every ovrin user, to run a programme none of them run.
module github.com/BAGOMBEKA-JOB-DEV/ovrin/examples/receipt

go 1.22

require (
	github.com/BAGOMBEKA-JOB-DEV/ovrin v0.1.0
	github.com/BAGOMBEKA-JOB-DEV/ovrin/model/skyl v0.1.0
)

require github.com/BAGOMBEKA-JOB-DEV/skyl v0.1.0 // indirect

// Nothing in this repository is published yet, so the require lines above name
// tags that do not exist. These point at the sibling directories instead.
//
// They come out the day ovrin is tagged. CI's `releasable` job rejects a
// replace directive on a tag build, which is what stops them being forgotten.
replace github.com/BAGOMBEKA-JOB-DEV/ovrin => ../..

replace github.com/BAGOMBEKA-JOB-DEV/ovrin/model/skyl => ../../model/skyl
