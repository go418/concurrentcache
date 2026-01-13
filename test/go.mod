module test

go 1.24.0

replace github.com/go418/concurrentcache => ../

replace github.com/go418/concurrentcache/logger => ../logger

require (
	github.com/go-logr/logr v1.4.2
	github.com/go418/concurrentcache v0.0.0-00010101000000-000000000000
	github.com/go418/concurrentcache/logger v0.0.0-00010101000000-000000000000
	github.com/stretchr/testify v1.9.0
	golang.org/x/sync v0.7.0
	k8s.io/klog/v2 v2.130.1
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
