package kube

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sort"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LogStream streams the release's pod logs to w. It selects the workload pods by
// the chart's instance label and, by default, streams only the NEWEST pod
// (most-recently-created). With opts.AllPods every pod is multiplexed and each
// line is prefixed with "[<pod>] ". With opts.Follow it forwards new lines until
// ctx is cancelled (e.g. client disconnect) or the stream ends; without Follow it
// writes a one-shot snapshot and returns. The caller flushes w per line.
func (b *KubeBackend) LogStream(ctx context.Context, namespace, release string, opts LogStreamOptions, w io.Writer) error {
	pods, err := b.streamPods(ctx, namespace, release, opts.AllPods)
	if err != nil {
		return err
	}
	if len(pods) == 0 {
		return fmt.Errorf("kube: no pods for release %q in %q", release, namespace)
	}

	logOpts := &corev1.PodLogOptions{
		Follow:   opts.Follow,
		Previous: opts.Previous,
	}
	if opts.TailLines > 0 {
		t := int64(opts.TailLines)
		logOpts.TailLines = &t
	}

	if len(pods) == 1 {
		return b.streamPod(ctx, namespace, pods[0], "", logOpts, w)
	}

	// Multiplex multiple pods concurrently; each line is prefixed with its pod
	// name. Writes are serialized through a mutex so interleaved lines stay whole.
	var mu sync.Mutex
	sw := &lockedWriter{mu: &mu, w: w}
	var wg sync.WaitGroup
	errs := make([]error, len(pods))
	for i, p := range pods {
		wg.Add(1)
		go func(i int, pod string) {
			defer wg.Done()
			errs[i] = b.streamPod(ctx, namespace, pod, "["+pod+"] ", logOpts, sw)
		}(i, p)
	}
	wg.Wait()
	// A cancelled context (client disconnect) is the normal stop condition, not a
	// failure; return the first non-context error if any.
	for _, e := range errs {
		if e != nil && ctx.Err() == nil {
			return e
		}
	}
	return nil
}

// streamPod opens a follow/snapshot log stream for one pod and copies it line by
// line to w (prefixing each line with prefix). It stops when ctx is cancelled.
func (b *KubeBackend) streamPod(ctx context.Context, namespace, pod, prefix string, opts *corev1.PodLogOptions, w io.Writer) error {
	rc, err := b.client.CoreV1().Pods(namespace).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		return fmt.Errorf("kube: stream logs for %s/%s: %w", namespace, pod, err)
	}
	// Closing the body unblocks a blocked Read when ctx is cancelled, so the
	// goroutine never leaks waiting on a follow stream the client abandoned.
	defer func() { _ = rc.Close() }()
	go func() {
		<-ctx.Done()
		_ = rc.Close()
	}()

	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Bytes()
		if prefix != "" {
			if _, err := io.WriteString(w, prefix); err != nil {
				return err
			}
		}
		if _, err := w.Write(line); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// streamPods lists the release's pods (newest first) and returns either just the
// newest pod or all of them depending on allPods.
func (b *KubeBackend) streamPods(ctx context.Context, namespace, release string, allPods bool) ([]string, error) {
	pods, err := b.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/instance=" + release,
	})
	if err != nil {
		return nil, err
	}
	items := pods.Items
	// Newest first by creation timestamp; break ties by name for determinism.
	sort.Slice(items, func(i, j int) bool {
		ti, tj := items[i].CreationTimestamp, items[j].CreationTimestamp
		if ti.Equal(&tj) {
			return items[i].Name < items[j].Name
		}
		return ti.After(tj.Time)
	})
	names := make([]string, 0, len(items))
	for i := range items {
		names = append(names, items[i].Name)
	}
	if !allPods && len(names) > 1 {
		names = names[:1]
	}
	return names, nil
}

// lockedWriter serializes concurrent writes from the multi-pod multiplexer so a
// single log line is never interleaved with another pod's mid-write.
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
