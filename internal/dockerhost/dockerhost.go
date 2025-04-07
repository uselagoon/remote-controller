package dockerhost

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/uselagoon/remote-controller/internal/helpers"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DockerHost struct {
	Client              client.Client
	Log                 logr.Logger
	DockerHostNamespace string
	ReuseType           string
	ReuseCache          *lru.Cache[string, string]
	BuildCache          *lru.Cache[string, string]
}

func (d *DockerHost) AssignDockerHost(buildName, reuseIdentifier string, qos bool, qosMax int) string {
	ctx := context.TODO()
	dockerHost := d.defaultHost()
	dockerHosts := &corev1.ServiceList{}
	listOption := (&client.ListOptions{}).ApplyOptions([]client.ListOption{
		client.InNamespace(d.DockerHostNamespace),
		client.MatchingLabels(map[string]string{"dockerhost.lagoon.sh/dedicated": "true"}),
	})
	// list all dockerhost ports
	if err := d.Client.List(ctx, dockerHosts, listOption); err != nil {
		// return the default dockerhost
		d.Log.Error(err, "unable to determine dockerhost, falling back to default dockerhost")
		return dockerHost
	}

	// check available dockerhosts
	availableHosts := []string{}
	sort.Slice(dockerHosts.Items, func(i, j int) bool { return dockerHosts.Items[i].Name < dockerHosts.Items[j].Name })
	for _, dh := range dockerHosts.Items {
		dhsvc := fmt.Sprintf("%s.%s.svc", dh.Name, d.DockerHostNamespace)
		for _, port := range dh.Spec.Ports {
			// network policy may need to be adjusted in chart to support a controller installed in a non lagoon namespace
			if tcpCheck(dhsvc, strconv.Itoa(int(port.Port))) {
				availableHosts = append(availableHosts, dhsvc)
			}
		}
	}

	if cacheHost, ok := d.ReuseCache.Get(reuseIdentifier); ok {
		if helpers.ContainsString(availableHosts, cacheHost) {
			// if the namespace has already got a host assigned, prefer it
			dockerHost = cacheHost
		}
	}

	// pick a host to use
	hostsInUse := countDupes(d.BuildCache.Values())
	dockerHost = d.pickHost(dockerHost, availableHosts, hostsInUse, qosMax, qos)
	// finally assign in the cache
	_ = d.BuildCache.Add(buildName, dockerHost)
	_ = d.ReuseCache.Add(reuseIdentifier, dockerHost)
	return dockerHost
}

func New(client client.Client,
	log logr.Logger,
	dockerHostNamespace,
	reuseType string,
	reuseCache,
	buildCache *lru.Cache[string, string],
) *DockerHost {
	return &DockerHost{
		Client:              client,
		Log:                 log,
		DockerHostNamespace: dockerHostNamespace,
		ReuseType:           reuseType,
		ReuseCache:          reuseCache,
		BuildCache:          buildCache,
	}
}

func (d *DockerHost) defaultHost() string {
	return fmt.Sprintf("docker-host.%s.svc", d.DockerHostNamespace) // default dockerhost
}

func tcpCheck(host string, port string) bool {
	timeout := 5 * time.Second
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return false
	}
	if conn != nil {
		defer conn.Close()
	}
	return true
}

func countDupes(list []string) map[string]int {
	dupeFreq := make(map[string]int)
	for _, item := range list {
		// check if the item/element exist in the map
		_, exist := dupeFreq[item]
		if exist {
			dupeFreq[item] += 1 // increase counter by 1 if already in the map
		} else {
			dupeFreq[item] = 1 // else start counting from 1
		}
	}
	return dupeFreq
}

// pick host will attempt to determine the most suitable docker host to use with limited information
// without deep inspecting the docker hosts for actual usage/consumption, just use the number of builds as an estimate
func (d *DockerHost) pickHost(chosenHost string, availableHosts []string, hostsInUse map[string]int, qosMax int, qos bool) string {
	buildsPerHost := 0
	numAvailableHosts := len(availableHosts)
	if numAvailableHosts == 0 {
		// return the default docker-host if no available hosts
		return d.defaultHost()
	}
	if qos {
		// if qos enabled, work out how many builds per host can be used
		buildsPerHost = qosMax / numAvailableHosts
		if buildsPerHost%2 != 0 {
			// add one so that if the number of builds is odd
			// it will favor building on the host it may have already run on
			// to try leverage cache where possible
			buildsPerHost = buildsPerHost + 1
		}
	} else {
		// work out how many builds per host based on distribution
		totBuilds := 0
		for _, boh := range hostsInUse {
			totBuilds = totBuilds + boh
		}
		if totBuilds > 0 {
			buildsPerHost = totBuilds / numAvailableHosts
			if buildsPerHost%2 != 0 {
				// add one so that if the number of builds is odd
				// it will favor building on the host it may have already run on
				// to try leverage cache where possible
				buildsPerHost = buildsPerHost + 1
			}
		} else {
			// default to 2, as builds increase this will be ignored
			// set to 2 just to allow builds to schedule properly somewhere
			buildsPerHost = 2
		}
	}
	for h := range hostsInUse {
		if !helpers.ContainsString(availableHosts, h) {
			// delete any no longer available hosts from the in use table to force selection of another
			delete(hostsInUse, h)
		}
	}
	for _, availableHost := range availableHosts {
		if _, ok := hostsInUse[availableHost]; !ok {
			// add any other available docker hosts with 0 to indicate they're not in use
			hostsInUse[availableHost] = 0
		}
	}
	dockerHosts := make([]string, 0, len(hostsInUse))
	for dockerHost := range hostsInUse {
		dockerHosts = append(dockerHosts, dockerHost)
	}
	// sort dockerhosts, lowest in use first
	sort.Slice(dockerHosts, func(i, j int) bool { return hostsInUse[dockerHosts[i]] < hostsInUse[dockerHosts[j]] })

	// work out the best host to use
	if buildsOnHost, ok := hostsInUse[chosenHost]; ok && buildsOnHost < buildsPerHost {
		// if the chosen host exists and has free build slots, use it
		return chosenHost
	}
	for _, host := range dockerHosts {
		buildsOnHost := hostsInUse[host]
		if host == chosenHost && buildsOnHost >= buildsPerHost {
			// chosen host is too busy
			continue
		} else if buildsOnHost >= buildsPerHost {
			// host is too busy
			continue
		} else {
			// next lowest host in use
			return host
		}
	}
	// just use the chosen host as the fall back
	return chosenHost
}
