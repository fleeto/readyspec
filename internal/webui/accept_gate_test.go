package webui

import "testing"

func TestStopAcceptingRunsWaitsForAcceptanceCriticalSection(t *testing.T) {
	server := &Server{accepting: true}
	server.acceptMu.RLock() // Mirrors submitRun's lock spanning durable acceptance.
	stopped := make(chan struct{})
	started := make(chan struct{})
	go func() {
		close(started)
		server.StopAcceptingRuns()
		close(stopped)
	}()
	<-started
	select {
	case <-stopped:
		t.Fatal("acceptance gate closed while an acceptance was in its critical section")
	default:
	}
	server.acceptMu.RUnlock()
	<-stopped
	if server.AcceptingRuns() {
		t.Fatal("worker stop did not close the acceptance gate")
	}
}
