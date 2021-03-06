// Copyright 2016 The prometheus-operator Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package listwatch

import (
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
)

var _ watch.Interface = &multiWatch{}

func setupMultiWatch(n int, t *testing.T, rvs string) ([]*watch.FakeWatcher, *multiWatch) {
	ws := make([]*watch.FakeWatcher, n)
	lws := make([]cache.ListerWatcher, n)
	for i := range ws {
		w := watch.NewFake()
		ws[i] = w
		lws[i] = &cache.ListWatch{WatchFunc: func(_ metav1.ListOptions) (watch.Interface, error) {
			return w, nil
		}}
	}
	m, err := newMultiWatch(lws, rvs, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("failed to create new multiWatch: %v", err)
	}
	return ws, m
}

func TestNewMultiWatch(t *testing.T) {
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("newMultiWatch should not panic when number of resource versions matches ListerWatchers; got: %v", r)
			}
		}()
		// Create a multiWatch from 1 ListerWatchers and pass 1 resource versions.
		_, _ = setupMultiWatch(1, t, "1")
	}()
}

func TestMultiWatchResultChan(t *testing.T) {
	ws, m := setupMultiWatch(10, t, "10")
	defer m.Stop()
	var events []watch.Event
	var wg sync.WaitGroup
	for _, w := range ws {
		w := w
		wg.Add(1)
		go func() {
			w.Add(&runtime.Unknown{})
		}()
	}
	go func() {
		for {
			event, ok := <-m.ResultChan()
			if !ok {
				break
			}
			events = append(events, event)
			wg.Done()
		}
	}()
	wg.Wait()
	if len(events) != len(ws) {
		t.Errorf("expected %d events but got %d", len(ws), len(events))
	}
}

func TestMultiWatchStop(t *testing.T) {
	ws, m := setupMultiWatch(10, t, "10")
	m.Stop()
	var stopped int
	for _, w := range ws {
		_, running := <-w.ResultChan()
		if !running && w.IsStopped() {
			stopped++
		}
	}
	if stopped != len(ws) {
		t.Errorf("expected %d watchers to be stopped but got %d", len(ws), stopped)
	}
	select {
	case <-m.stopped:
		// all good, watcher is closed, proceed
	default:
		t.Error("expected multiWatch to be stopped")
	}
	_, running := <-m.ResultChan()
	if running {
		t.Errorf("expected multiWatch chan to be closed")
	}
}

type mockListerWatcher struct {
	listResult runtime.Object
	evCh       chan watch.Event
	stopped    bool
}

func (m *mockListerWatcher) List(options metav1.ListOptions) (runtime.Object, error) {
	return m.listResult, nil
}

func (m *mockListerWatcher) Watch(options metav1.ListOptions) (watch.Interface, error) {
	return m, nil
}

func (m *mockListerWatcher) Stop() {
	m.stopped = true
}

func (m *mockListerWatcher) ResultChan() <-chan watch.Event {
	return m.evCh
}

func TestRacyMultiWatch(t *testing.T) {
	evCh := make(chan watch.Event)
	lw := &mockListerWatcher{evCh: evCh}

	mw, err := newMultiWatch(
		[]cache.ListerWatcher{lw},
		"foo",
		metav1.ListOptions{},
	)
	if err != nil {
		t.Error(err)
		return
	}

	// this will not block, as newMultiWatch started a goroutine,
	// receiving that event and block on the dispatching it there.
	evCh <- watch.Event{
		Type: "foo",
	}

	if got := <-mw.ResultChan(); got.Type != "foo" {
		t.Errorf("expected foo, got %s", got.Type)
		return
	}

	// Enqueue event, do not dequeue it.
	// In conjunction with go test -race this asserts
	// if there is a race between stopping and dispatching an event
	evCh <- watch.Event{
		Type: "bar",
	}
	mw.Stop()

	if got := lw.stopped; got != true {
		t.Errorf("expected watcher to be closed true, got %t", got)
	}

	// some reentrant calls, should be non-blocking
	mw.Stop()
	mw.Stop()
}

func TestIdenticalNamespaces(t *testing.T) {
	for _, tc := range []struct {
		a, b map[string]struct{}
		ret  bool
	}{
		{
			a: map[string]struct{}{
				"foo": struct{}{},
			},
			b: map[string]struct{}{
				"foo": struct{}{},
			},
			ret: true,
		},
		{
			a: map[string]struct{}{
				"foo": struct{}{},
			},
			b: map[string]struct{}{
				"bar": struct{}{},
			},
			ret: false,
		},
	} {
		tc := tc
		t.Run("", func(t *testing.T) {
			ret := IdenticalNamespaces(tc.a, tc.b)
			if ret != tc.ret {
				t.Fatalf("expecting IdenticalNamespaces() to return %v, got %v", tc.ret, ret)
			}
		})
	}
}
