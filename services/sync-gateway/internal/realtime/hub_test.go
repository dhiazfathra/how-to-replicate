package realtime

import "testing"

func TestHub_NotifyWakesSubscriber(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("ws1")
	defer cancel()

	h.Notify("ws1")

	select {
	case <-ch:
	default:
		t.Fatalf("want subscriber woken, channel empty")
	}
}

func TestHub_NotifyIsWorkspaceScoped(t *testing.T) {
	h := NewHub()
	chA, cancelA := h.Subscribe("wsA")
	defer cancelA()
	chB, cancelB := h.Subscribe("wsB")
	defer cancelB()

	h.Notify("wsA")

	select {
	case <-chA:
	default:
		t.Fatalf("want wsA's subscriber woken")
	}
	select {
	case <-chB:
		t.Fatalf("want wsB's subscriber untouched by a wsA notify")
	default:
	}
}

func TestHub_NotifyCoalescesForUnreadSubscriber(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("ws1")
	defer cancel()

	// Two notifies with no read in between must not block or panic — the
	// channel has capacity 1 and the second send is dropped, not queued.
	h.Notify("ws1")
	h.Notify("ws1")

	select {
	case <-ch:
	default:
		t.Fatalf("want at least one pending notification")
	}
	select {
	case <-ch:
		t.Fatalf("want the second notify coalesced away, not queued")
	default:
	}
}

func TestHub_NotifyWithNoSubscribersIsANoop(t *testing.T) {
	h := NewHub()
	h.Notify("nobody-subscribed")
}

func TestHub_CancelStopsFurtherNotifies(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("ws1")
	cancel()

	h.Notify("ws1")

	select {
	case v, ok := <-ch:
		t.Fatalf("want no notify after cancel, got %v ok=%v", v, ok)
	default:
	}
}

func TestHub_MultipleSubscribersSameWorkspace(t *testing.T) {
	h := NewHub()
	ch1, cancel1 := h.Subscribe("ws1")
	defer cancel1()
	ch2, cancel2 := h.Subscribe("ws1")
	defer cancel2()

	h.Notify("ws1")

	for i, ch := range []chan struct{}{ch1, ch2} {
		select {
		case <-ch:
		default:
			t.Fatalf("subscriber %d not woken", i)
		}
	}
}
