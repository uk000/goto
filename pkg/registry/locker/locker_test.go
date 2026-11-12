package locker

// Unit tests for locker.go: label validation, opening and closing lockers, marshaling, and concurrent use.
// The concurrency tests are meant to be run with -race.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
)

func TestValidateLabel(t *testing.T) {
	for _, label := range []string{"a", "a,b", "a,b,c", "with space", "default"} {
		if err := ValidateLabel(label); err != nil {
			t.Errorf("ValidateLabel(%q) = %v, want nil", label, err)
		}
	}
	for _, label := range []string{"", ",", ",x", "x,", "x,,y", ",,"} {
		if err := ValidateLabel(label); err == nil {
			t.Errorf("ValidateLabel(%q) = nil, want an error", label)
		}
	}
}

// a label with an empty segment used to nil out the current locker.
func TestOpenLockerRejectsEmptySegments(t *testing.T) {
	for _, label := range []string{"", ",x", "x,", "x,,y", "a,,"} {
		ll := NewLabeledLockers()
		if err := ll.OpenLocker(label); err == nil {
			t.Errorf("OpenLocker(%q) = nil, want an error", label)
		}
		current := ll.GetLocker("current")
		if current == nil || current.Label != "default" || !current.isCurrent() {
			t.Fatalf("OpenLocker(%q) left the current locker as %+v, want the default locker, still current", label, current)
		}
		if ll.GetCurrentLocker() != current {
			t.Errorf("OpenLocker(%q): GetCurrentLocker() != GetLocker(current)", label)
		}
		if len(ll.GetLockerLabels()) != 1 {
			t.Errorf("OpenLocker(%q) created lockers: %v", label, ll.GetLockerLabels())
		}
		if cl := ll.GetOrCreateLocker(label); cl != nil {
			t.Errorf("GetOrCreateLocker(%q) = %+v, want nil", label, cl)
		}
		if len(ll.GetLockerLabels()) != 1 {
			t.Errorf("GetOrCreateLocker(%q) created lockers: %v", label, ll.GetLockerLabels())
		}
		// the lockers keep working
		if got := ll.GetAllLockers(true, true, 0); len(got) != 1 || got["default"] == nil {
			t.Errorf("OpenLocker(%q): lockers = %v, want just the default one", label, got)
		}
	}
}

// A partially valid label used to create the lockers up to the empty segment before it gave up.
func TestInvalidLabelCreatesNothing(t *testing.T) {
	ll := NewLabeledLockers()
	if cl := ll.GetOrCreateLocker("a,b,,c"); cl != nil {
		t.Fatalf("GetOrCreateLocker(a,b,,c) = %+v, want nil", cl)
	}
	if err := ll.OpenLocker("a,b,,c"); err == nil {
		t.Fatal("OpenLocker(a,b,,c) = nil, want an error")
	}
	if ll.GetLocker("a") != nil || ll.GetLocker("a,b") != nil {
		t.Errorf("lockers were created for an invalid label: %v", ll.GetLockerLabels())
	}
}

func TestOpenLockerSwitchesCurrent(t *testing.T) {
	ll := NewLabeledLockers()
	if err := ll.OpenLocker("a,b"); err != nil {
		t.Fatal(err)
	}
	if got := ll.GetLocker("current"); got == nil || got.PathLabel != "a,b" {
		t.Fatalf("current = %+v, want a,b", got)
	}
	if ll.GetLocker("default").isCurrent() {
		t.Error("default locker is still flagged current after opening a,b")
	}
	if err := ll.OpenLocker("default"); err != nil {
		t.Fatal(err)
	}
	if got := ll.GetLocker("current"); got == nil || got.Label != "default" || !got.isCurrent() {
		t.Fatalf("current = %+v, want default", got)
	}
}

// Closing a locker that holds the current locker must not leave the current locker detached from the lockers.
func TestCloseLockerAboveCurrentFallsBackToDefault(t *testing.T) {
	ll := NewLabeledLockers()
	if err := ll.OpenLocker("a,b"); err != nil {
		t.Fatal(err)
	}
	ll.ClearLocker("a", true)
	current := ll.GetLocker("current")
	if current == nil || current.Label != "default" || !current.isCurrent() {
		t.Fatalf("current after closing a = %+v, want the default locker", current)
	}
	if ll.GetLocker("a") != nil || ll.GetLocker("a,b") != nil {
		t.Errorf("closed lockers are still there: %v", ll.GetLockerLabels())
	}
	// closing the default locker (not possible through the API) must not leave a nil current locker either
	ll.ClearLocker("default", true)
	if current := ll.GetLocker("current"); current == nil || current.Label != "default" {
		t.Fatalf("current after closing default = %+v, want a fresh default locker", current)
	}
}

func TestClearLockerKeepsCurrentFlag(t *testing.T) {
	ll := NewLabeledLockers()
	ll.GetOrCreateLocker("a,b").Store([]string{"k"}, "v", false)
	// a locker is flagged current when it is created, so unflag it to see that clearing doesn't flag it again
	ll.GetLocker("a").setCurrent(false)
	ll.ClearLocker("a", false)
	if ll.GetLocker("a,b") != nil {
		t.Error("sub-locker a,b survived clearing a")
	}
	if ll.GetLocker("a").isCurrent() {
		t.Error("clearing a made it current")
	}
	if !ll.GetLocker("default").isCurrent() {
		t.Error("clearing a took the current flag from the default locker")
	}
}

// The marshaled forms must stay what they were: the dump format is read back by the unmarshalers.
func TestMarshalStillRoundTrips(t *testing.T) {
	ll := NewLabeledLockers()
	ll.GetOrCreateLocker("L1,sub").Store([]string{"k", "k2"}, "v", false)
	ll.GetOrCreateLocker("L1").StorePeerData("p", "", []string{"pk"}, "pv")
	ll.GetOrCreateLocker("L1").StorePeerData("p", "inst1", []string{"ik"}, "iv")
	b, err := json.Marshal(ll.GetAllLockers(true, true, 0))
	if err != nil {
		t.Fatal(err)
	}
	restored := NewLabeledLockers()
	lockers := CombiLockers{}
	if err := json.Unmarshal(b, &lockers); err != nil {
		t.Fatal(err)
	}
	restored.ReplaceLockers(lockers)
	b2, err := json.Marshal(restored.GetAllLockers(true, true, 0))
	if err != nil {
		t.Fatal(err)
	}
	var m1, m2 interface{}
	json.Unmarshal(b, &m1)
	json.Unmarshal(b2, &m2)
	if fmt.Sprint(m1) != fmt.Sprint(m2) {
		t.Errorf("round trip changed the lockers:\n before: %s\n after:  %s", b, b2)
	}
}

// runConcurrently runs the writers and readers in parallel until the writers are done.
func runConcurrently(t *testing.T, writers, readers []func(i int), iterations int) {
	t.Helper()
	var wg sync.WaitGroup
	done := make(chan struct{})
	var readersWG sync.WaitGroup
	for _, read := range readers {
		read := read
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for i := 0; ; i++ {
				select {
				case <-done:
					return
				default:
					read(i)
				}
			}
		}()
	}
	for _, write := range writers {
		write := write
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				write(i)
			}
		}()
	}
	wg.Wait()
	close(done)
	readersWG.Wait()
}

// sub-lockers were created and deleted without the owning locker's lock, while views, marshaling and
// searches read the sub-lockers map under it.
func TestConcurrentSubLockersCreateDeleteAndRead(t *testing.T) {
	ll := NewLabeledLockers()
	ll.OpenLocker("p")
	keys := []string{"k"}
	runConcurrently(t,
		[]func(int){
			func(i int) { ll.OpenLocker("p," + strconv.Itoa(i%50)) },
			func(i int) {
				ll.GetOrCreateLocker("p,"+strconv.Itoa(i%50)+",deep").Store(keys, "v", false)
			},
			func(i int) { ll.ClearLocker("p,"+strconv.Itoa(i%50), i%2 == 0) },
		},
		[]func(int){
			func(int) { json.Marshal(ll.GetAllLockers(true, true, 0)) },
			func(int) { ll.GetAllLockers(true, true, 2) },
			func(int) { ll.GetAllLockers(false, false, 0) },
			func(int) { ll.GetLockerLabels() },
			func(int) { ll.Get("p", keys, true, 0) },
			func(int) { ll.Get("all", keys, true, 0) },
			func(int) { ll.GetDataLockerPaths("p", true) },
			func(int) { ll.GetDataLockers("all") },
			func(int) { ll.GetPeerLockers("") },
			func(int) {
				if cl := ll.GetLocker("p"); cl != nil {
					cl.GetLockerView(true)
					cl.Trim(2)
				}
			},
		}, 300)
	if cur := ll.GetLocker("current"); cur == nil {
		t.Fatal("no current locker after the concurrent opens and closes")
	}
}

// the contexts map was iterated by GetAllLockers without the lock that GetContextLocker adds to it under.
func TestConcurrentContextLockers(t *testing.T) {
	cl := NewContextLockers()
	runConcurrently(t,
		[]func(int){
			func(i int) { cl.GetContextLocker("ctx" + strconv.Itoa(i%100)).OpenLocker("l") },
			func(i int) { cl.ClearContextLocker("ctx" + strconv.Itoa(i%100)) },
		},
		[]func(int){
			func(int) { cl.GetAllLockers(true, true, 1) },
			func(int) { cl.GetAllLockers(false, false, 0) },
			func(int) { cl.GetAllLockers(true, true, 0) },
		}, 300)
	if got := cl.GetAllLockers(true, true, 1); len(got) == 0 {
		t.Error("no contexts reported")
	}
}

// peer locker views, trims and the instance lockers' data were read without the lock that stores take.
func TestConcurrentPeerStoresAndViews(t *testing.T) {
	ll := NewLabeledLockers()
	cl := ll.GetCurrentLocker()
	runConcurrently(t,
		[]func(int){
			func(i int) { cl.StorePeerData("p", "a1", []string{"k", strconv.Itoa(i % 20), "x"}, "v") },
			func(i int) { cl.StorePeerData("p", "", []string{"own", strconv.Itoa(i % 20), "y"}, "v") },
			func(i int) { cl.Store([]string{"d", strconv.Itoa(i % 20), "z"}, "v", false) },
			func(i int) { cl.RemovePeerData("p", "a1", []string{"k", strconv.Itoa(i % 20)}) },
			func(i int) { cl.DeactivateInstanceLocker("p", "a1") },
			func(i int) { cl.InitPeerLocker("p", "a2") },
		},
		[]func(int){
			func(int) { cl.Trim(2) },
			func(int) { cl.Trim(1) },
			func(int) { cl.GetLockerView(false) },
			func(int) { cl.GetLockerView(true) },
			func(int) { cl.GetLockerWithoutPeers() },
			func(int) { cl.GetDataLockerView() },
			func(int) { cl.GetPeerLockersView("p", "a1", true) },
			func(int) { cl.GetPeerLockersView("", "", false) },
			func(int) { cl.SearchOrGetDataLockerPaths("/x/", nil, true) },
			func(int) { cl.GetFromPeerInstanceLocker("p", "", []string{"k"}, true, 0) },
			func(int) {
				if b, err := json.Marshal(cl); err != nil || len(b) == 0 {
					t.Errorf("marshal failed: %v", err)
				}
			},
			func(int) {
				for _, pls := range cl.GetPeerLockers("p", "") {
					for _, pl := range pls {
						json.Marshal(pl)
					}
				}
			},
			func(int) {
				for _, dl := range ll.GetDataLockers("all") {
					json.Marshal(dl)
				}
			},
		}, 300)
}

// The same readers against the shared lockers, as the registry's peers reset them.
func TestConcurrentInitPeerLockerAndReaders(t *testing.T) {
	ll := NewLabeledLockers()
	cl := ll.GetCurrentLocker()
	runConcurrently(t,
		[]func(int){
			func(i int) { cl.InitPeerLocker("", "") },
			func(i int) { cl.StorePeerData("p"+strconv.Itoa(i%5), "a", []string{"k"}, "v") },
		},
		[]func(int){
			func(int) { cl.GetLockerView(true) },
			func(int) { cl.Trim(2) },
			func(int) { json.Marshal(cl) },
			func(int) { cl.GetPeerLockers("", "") },
		}, 300)
}

// reading a key path that holds only sub-keys returned the live entry, which the caller encoded after the
// data locker's lock was released, while stores and removes updated its sub-keys.
func TestConcurrentStoresAndGetsOfSubKeyEntries(t *testing.T) {
	ll := NewLabeledLockers()
	cl := ll.GetCurrentLocker()
	keys := []string{"k"}
	encode := func(v interface{}) {
		if v != nil {
			if _, err := json.Marshal(v); err != nil {
				t.Errorf("marshal failed: %v", err)
			}
		}
	}
	runConcurrently(t,
		[]func(int){
			func(i int) { cl.Store([]string{"k", strconv.Itoa(i % 20), "x"}, "v", false) },
			func(i int) { cl.StorePeerData("p", "a1", []string{"k", strconv.Itoa(i % 20), "x"}, "v") },
			func(i int) { cl.StorePeerData("p", "", []string{"k", strconv.Itoa(i % 20), "x"}, "v") },
			func(i int) { cl.Remove([]string{"k", strconv.Itoa(i % 20)}) },
			func(i int) { cl.RemovePeerData("p", "a1", []string{"k", strconv.Itoa(i % 20)}) },
		},
		[]func(int){
			func(int) { v, _ := ll.Get("current", keys, true, 0); encode(v) },
			func(int) { v, _ := ll.GetFromPeerInstanceLocker("current", "p", "a1", keys, true, 0); encode(v) },
			func(int) { v, _, _ := cl.GetFromPeerInstanceLocker("p", "", keys, true, 0); encode(v) },
			func(int) { v, _, _ := cl.Get(keys, true, 0); encode(v) },
		}, 300)
}

func TestGetReturnsACopyOfSubKeyEntries(t *testing.T) {
	ll := NewLabeledLockers()
	cl := ll.GetCurrentLocker()
	cl.Store([]string{"k", "a"}, "1", false)
	v, _, _ := cl.Get([]string{"k"}, true, 0)
	cl.Store([]string{"k", "b"}, "2", false)
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]interface{}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	if _, present := got["k"]["b"]; present {
		t.Errorf("the value read earlier shows a later store: %s", b)
	}
	if got["k"]["a"] == nil {
		t.Errorf("the value read earlier is missing sub-key a: %s", b)
	}
}
