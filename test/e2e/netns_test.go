//go:build linux && e2e

// Package e2e holds the end-to-end tests, one file per feature.
//
// These tests need root and are excluded from the default build. Run them with:
//
//	make test-e2e
//	make test-e2e RUN=<regexp>
//
// Every test runs on a throwaway network namespace that stands in for the node,
// so the host's own bridge and interfaces are never touched.
//
// This file is the namespace machinery, and knows nothing about tiny-cni: it
// would look the same for any project wiring up Linux network namespaces. What
// knows about the plugin lives in plugin_test.go.
package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

func requireRoot(t *testing.T) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("e2e tests need root to create network namespaces")
	}
}

// enterNodeNetns moves the calling test onto a private network namespace that
// plays the role of the node. The test goroutine is pinned to its thread for
// the whole run so that every netlink call lands in that namespace.
func enterNodeNetns(t *testing.T) {
	t.Helper()

	runtime.LockOSThread()

	orig, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		runtime.UnlockOSThread()
		t.Fatalf("opening current netns: %v", err)
	}

	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		orig.Close()
		runtime.UnlockOSThread()
		t.Fatalf("unsharing netns: %v", err)
	}

	t.Cleanup(func() {
		defer orig.Close()
		if err := unix.Setns(int(orig.Fd()), unix.CLONE_NEWNET); err != nil {
			// the thread is stuck in the test namespace, leave it locked so
			// the runtime retires it instead of handing it back to the pool
			t.Errorf("restoring original netns: %v", err)
			return
		}
		runtime.UnlockOSThread()
	})
}

// newPodNetns creates a persistent network namespace and returns the path of
// the file the CNI plugin is expected to receive as CNI_NETNS. It mimics what
// `ip netns add` does: bind mounting the namespace of a thread onto a file
// keeps it alive once that thread is gone.
//
// The namespace comes back empty, with no interface but a down loopback, the
// way a container runtime would hand it to the plugin.
func newPodNetns(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating netns mount point %s: %v", path, err)
	}
	f.Close()

	ch := make(chan error, 1)
	go func() {
		// on any failure past the unshare we return without unlocking, so the
		// runtime destroys the tainted thread rather than reusing it
		runtime.LockOSThread()

		orig, err := os.Open("/proc/thread-self/ns/net")
		if err != nil {
			runtime.UnlockOSThread()
			ch <- fmt.Errorf("opening current netns: %w", err)
			return
		}
		defer orig.Close()

		if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
			runtime.UnlockOSThread()
			ch <- fmt.Errorf("unsharing netns: %w", err)
			return
		}

		if err := unix.Mount("/proc/thread-self/ns/net", path, "none", unix.MS_BIND, ""); err != nil {
			ch <- fmt.Errorf("bind mounting netns on %s: %w", path, err)
			return
		}

		if err := unix.Setns(int(orig.Fd()), unix.CLONE_NEWNET); err != nil {
			ch <- fmt.Errorf("restoring netns: %w", err)
			return
		}

		runtime.UnlockOSThread()
		ch <- nil
	}()

	if err := <-ch; err != nil {
		t.Fatalf("creating pod netns %s: %v", name, err)
	}

	t.Cleanup(func() {
		if err := unix.Unmount(path, 0); err != nil {
			t.Errorf("unmounting pod netns %s: %v", path, err)
		}
	})

	return path
}

// inNetns runs fn on a dedicated thread switched to the namespace at nsPath.
// The thread is never unlocked, so it dies with the goroutine and cannot leak
// the namespace back into the runtime's thread pool.
func inNetns(nsPath string, fn func() error) error {
	ch := make(chan error, 1)

	go func() {
		runtime.LockOSThread()

		f, err := os.Open(nsPath)
		if err != nil {
			ch <- fmt.Errorf("opening netns %s: %w", nsPath, err)
			return
		}
		defer f.Close()

		if err := unix.Setns(int(f.Fd()), unix.CLONE_NEWNET); err != nil {
			ch <- fmt.Errorf("switching to netns %s: %w", nsPath, err)
			return
		}

		ch <- fn()
	}()

	return <-ch
}
