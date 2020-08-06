module github.com/amazeeio/lagoon-kbd

go 1.13

require (
	github.com/cheshir/go-mq v1.0.2
	github.com/fsouza/go-dockerclient v1.6.5 // indirect
	github.com/go-logr/logr v0.1.0
	github.com/onsi/ginkgo v1.10.1
	github.com/onsi/gomega v1.7.0
	github.com/tiago4orion/conjure v0.0.0-20150908101743-93cb30b9d218 // indirect
	gopkg.in/matryer/try.v1 v1.0.0-20150601225556-312d2599e12e
	gopkg.in/robfig/cron.v2 v2.0.0-20150107220207-be2e0b0deed5
	k8s.io/api v0.0.0-20190918155943-95b840bb6a1f
	k8s.io/apimachinery v0.0.0-20190913080033-27d36303b655
	k8s.io/client-go v0.0.0-20190918160344-1fbdaa4c8d90
	sigs.k8s.io/controller-runtime v0.4.0
)

// Fixes for AppID
replace github.com/cheshir/go-mq v1.0.2 => github.com/shreddedbacon/go-mq v0.0.0-20200419104937-b8e9af912ead

replace github.com/NeowayLabs/wabbit v0.0.0-20200409220312-12e68ab5b0c6 => github.com/shreddedbacon/wabbit v0.0.0-20200419104837-5b7b769d7204
