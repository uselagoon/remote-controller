module github.com/uselagoon/remote-controller

go 1.16

require (
	cloud.google.com/go v0.81.0 // indirect
	github.com/cheekybits/is v0.0.0-20150225183255-68e9c0620927 // indirect
	github.com/cheshir/go-mq v1.0.2
	github.com/fsouza/go-dockerclient v1.6.5 // indirect
	github.com/go-logr/logr v0.4.0
	github.com/matryer/try v0.0.0-20161228173917-9ac251b645a2 // indirect
	github.com/mittwald/goharbor-client/v3 v3.3.0
	github.com/mittwald/goharbor-client/v5 v5.0.3
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.14.0
	github.com/openshift/api v3.9.0+incompatible
	github.com/tiago4orion/conjure v0.0.0-20150908101743-93cb30b9d218 // indirect
	github.com/xhit/go-str2duration/v2 v2.0.0
	golang.org/x/oauth2 v0.0.0-20210402161424-2e8d93401602 // indirect
	gopkg.in/matryer/try.v1 v1.0.0-20150601225556-312d2599e12e
	gopkg.in/robfig/cron.v2 v2.0.0-20150107220207-be2e0b0deed5
	k8s.io/api v0.21.3
	k8s.io/apimachinery v0.21.3
	k8s.io/client-go v0.21.3
	sigs.k8s.io/controller-runtime v0.9.6
)

// Fixes for AppID
replace github.com/cheshir/go-mq v1.0.2 => github.com/shreddedbacon/go-mq v0.0.0-20200419104937-b8e9af912ead

replace github.com/NeowayLabs/wabbit v0.0.0-20200409220312-12e68ab5b0c6 => github.com/shreddedbacon/wabbit v0.0.0-20200419104837-5b7b769d7204

// includes page_size 100 for listing projects
replace github.com/mittwald/goharbor-client/v3 v3.3.0 => github.com/shreddedbacon/goharbor-client/v3 v3.0.0-20210618042159-ceb1f437ad75

// includes fixes for broken robot accounts
replace github.com/mittwald/goharbor-client/v5 v5.0.3 => github.com/manhtukhang/goharbor-client/v5 v5.0.0-20220412103901-87d1d79b12db
