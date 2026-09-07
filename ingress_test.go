package main

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
)

func TestIngressBoundedAndCommandPriority(t *testing.T) {
	i := newIngress()
	ctx := context.Background()
	for n := 0; n < 1000; n++ {
		i.Handle(ctx, &tg.UpdateShortMessage{ID: n, UserID: 8, Message: "hello"})
	}
	i.Handle(ctx, &tg.UpdateShortMessage{ID: 1001, UserID: 7, Out: true, Message: "/off"})
	if len(i.normal) != 64 || len(i.controls) != 1 || i.dropped.Load() != 936 {
		t.Fatal("ingress is not bounded")
	}
	r, _ := testRelay(t)
	// Controls are selected first, and authority is still enforced in the relay.
	r.Handle(ctx, <-i.controls)
	for len(i.normal) > 0 {
		r.Handle(ctx, <-i.normal)
	}
	if len(r.notify.targets[0].queue) != 0 {
		t.Fatal("queued normal messages bypassed off")
	}
}
