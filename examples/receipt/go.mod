// examples/receipt is its own module for the same reason every adapter is:
// it imports model/skyl, and a module's dependencies are inherited by everyone
// who imports it. Keeping the example in the root module would put skyl into
// the go.sum of every ovrin user, to run a programme none of them run.
module github.com/BAGOMBEKA-JOB-DEV/ovrin/examples/receipt

go 1.22

require (
	github.com/BAGOMBEKA-JOB-DEV/ovrin v0.3.0
	github.com/BAGOMBEKA-JOB-DEV/ovrin/model/skyl v0.1.0
)

require github.com/BAGOMBEKA-JOB-DEV/skyl v0.1.0 // indirect
