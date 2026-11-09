package registry

// Unit tests for registry.go.

import (
	"testing"
)

func TestNormalizePeers(t *testing.T) {
	peers := normalizePeers(map[string]*Peers{
		"nil":     nil,
		"noMaps":  {},
		"nilPod":  {Name: "nilPod", Pods: map[string]*Pod{"x": nil, "y": {}}},
		"epochs":  {PodEpochs: map[string][]*PodEpoch{"a": {nil, {Epoch: 1}}, "b": {nil}, "c": nil}},
		"offline": {Pods: map[string]*Pod{"z": {Address: "z"}}},
	}, true)
	if _, present := peers["nil"]; present {
		t.Error("nil peer was kept")
	}
	for name, p := range peers {
		if p.Pods == nil || p.PodEpochs == nil {
			t.Errorf("%s: pods or epochs are nil: %+v", name, p)
		}
		if p.Name == "" {
			t.Errorf("%s: name is empty", name)
		}
	}
	if _, present := peers["nilPod"].Pods["x"]; present {
		t.Error("nil pod was kept")
	}
	if y := peers["nilPod"].Pods["y"]; y == nil || y.Address != "y" || !y.Offline {
		t.Errorf("pod y = %+v, want address y, offline", y)
	}
	if got := peers["epochs"].PodEpochs; len(got) != 1 || len(got["a"]) != 1 || got["a"][0].Epoch != 1 {
		t.Errorf("epochs = %+v, want only the non-nil epoch of a", got)
	}
	notOffline := normalizePeers(map[string]*Peers{"p": {Pods: map[string]*Pod{"z": {}}}}, false)
	if notOffline["p"].Pods["z"].Offline {
		t.Error("pod was marked offline when not asked to")
	}
}
