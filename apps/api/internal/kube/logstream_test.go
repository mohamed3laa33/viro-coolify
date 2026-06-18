package kube

import (
	"bytes"
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// TestLogStreamSnapshotWritesLines asserts a non-follow LogStream writes the
// (fake) pod log body to the writer.
func TestLogStreamSnapshotWritesLines(t *testing.T) {
	ns := "vortex-acme-web"
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:      "api-abc",
		Namespace: ns,
		Labels:    map[string]string{"app.kubernetes.io/instance": "api"},
	}}
	cs := k8sfake.NewSimpleClientset(pod)
	b := NewWithClient(testConfig(), cs, &mockHelm{})

	var buf bytes.Buffer
	if err := b.LogStream(context.Background(), ns, "api", LogStreamOptions{TailLines: 10}, &buf); err != nil {
		t.Fatalf("LogStream: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected non-empty streamed log output")
	}
}

// TestStreamPodsSelectsNewest asserts the pod selector returns the newest pod
// first and, by default, only that pod.
func TestStreamPodsSelectsNewest(t *testing.T) {
	ns := "vortex-acme-web"
	older := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "api-old", Namespace: ns,
		Labels:            map[string]string{"app.kubernetes.io/instance": "api"},
		CreationTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
	}}
	newer := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "api-new", Namespace: ns,
		Labels:            map[string]string{"app.kubernetes.io/instance": "api"},
		CreationTimestamp: metav1.NewTime(time.Now()),
	}}
	cs := k8sfake.NewSimpleClientset(older, newer)
	b := NewWithClient(testConfig(), cs, &mockHelm{})

	one, err := b.streamPods(context.Background(), ns, "api", false)
	if err != nil {
		t.Fatalf("streamPods: %v", err)
	}
	if len(one) != 1 || one[0] != "api-new" {
		t.Fatalf("newest-only = %v, want [api-new]", one)
	}
	all, err := b.streamPods(context.Background(), ns, "api", true)
	if err != nil {
		t.Fatalf("streamPods all: %v", err)
	}
	if len(all) != 2 || all[0] != "api-new" {
		t.Fatalf("all pods = %v, want newest-first [api-new api-old]", all)
	}
}

// TestLogStreamNoPods asserts a release with no pods is an error (no silent
// empty success that would mask a misconfiguration).
func TestLogStreamNoPods(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	b := NewWithClient(testConfig(), cs, &mockHelm{})
	var buf bytes.Buffer
	if err := b.LogStream(context.Background(), "vortex-acme-web", "api", LogStreamOptions{}, &buf); err == nil {
		t.Fatal("expected error when no pods match the release")
	}
}
