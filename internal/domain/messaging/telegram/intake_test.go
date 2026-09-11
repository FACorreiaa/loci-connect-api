package telegram

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestSeenRemembersAndForgets(t *testing.T) {
	s := newSeen()

	if !s.first(1) {
		t.Error("the first sighting of an id should be new")
	}
	if s.first(1) {
		t.Error("the second sighting of an id should not be")
	}

	// Zero is Telegram's "no id"; it must not collapse every such update into
	// one that has already been handled.
	for attempt := range 3 {
		if !s.first(0) {
			t.Errorf("an update with no id was dropped on attempt %d", attempt+1)
		}
	}

	// Past capacity the oldest is forgotten, which is the accepted cost of a
	// fixed amount of memory.
	for id := int64(100); id < 100+seenCapacity; id++ {
		s.first(id)
	}
	if s.first(100 + seenCapacity - 1) {
		t.Error("the most recent id was forgotten")
	}
	if len(s.ids) > seenCapacity {
		t.Errorf("remembering %d ids, past the %d cap", len(s.ids), seenCapacity)
	}
}

// Answering a recording twice is two downloads, two transcriptions and two
// voice notes in the chat. Telegram redelivers whatever it does not get a
// prompt 200 for, and this handler answers long after it has replied.
func TestARedeliveredUpdateIsAnsweredOnce(t *testing.T) {
	api := newFakeAPI(t)
	handler := &recordingHandler{}
	hook := newWebhook(t, api, handler)

	body := encoded(t, update(7, 4242, "three days in Lisbon"))

	first := deliver(hook, http.MethodPost, testSecret, body)
	waitFor(t, func() bool { return len(handler.texts()) == 1 })

	second := deliver(hook, http.MethodPost, testSecret, body)

	// Both acknowledged: a non-2xx would make Telegram retry it further.
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Errorf("statuses were %d and %d, want 200 twice", first.Code, second.Code)
	}
	if got := handler.texts(); len(got) != 1 {
		t.Errorf("handled %d times, want once: %v", len(got), got)
	}
}

func TestTooManyUpdatesAtOnceAreAnsweredRatherThanDropped(t *testing.T) {
	api := newFakeAPI(t)
	handler := &blockingHandler{release: make(chan struct{})}
	hook := NewWebhook(api.client(), handler, testSecret, 2, nil)
	defer close(handler.release)

	// Fill both slots. Each update comes from its own chat, so the per-chat
	// limiter is not what is being measured here.
	for i := int64(1); i <= 2; i++ {
		deliver(hook, http.MethodPost, testSecret, encoded(t, update(i, 1000+i, "a plan")))
	}
	waitFor(t, func() bool { return handler.inFlight() == 2 })

	turned := deliver(hook, http.MethodPost, testSecret, encoded(t, update(3, 1003, "a plan")))

	// Still a 200: a non-2xx makes Telegram back off and, over a sustained
	// error rate, stop delivering at all. Being busy must not look broken.
	if turned.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 even at capacity", turned.Code)
	}

	// And the sender is told, rather than met with silence.
	waitFor(t, func() bool { return len(api.callsTo("sendMessage")) > 0 })
	sent, _ := api.callsTo("sendMessage")[0].body["text"].(string)
	if !strings.Contains(strings.ToLower(sent), "hands full") {
		t.Errorf("the busy reply said %q", sent)
	}

	if handler.inFlight() != 2 {
		t.Errorf("%d updates were in flight, want the cap of 2", handler.inFlight())
	}
}

// The daily quota is a counter and says nothing about a burst: an account with
// requests left can still send twenty recordings in ten seconds.
func TestOneChatCannotOutrunTheBot(t *testing.T) {
	api := newFakeAPI(t)
	handler := &recordingHandler{}
	hook := newWebhook(t, api, handler)

	for i := int64(1); i <= chatBurst+1; i++ {
		deliver(hook, http.MethodPost, testSecret, encoded(t, update(i, 4242, "a plan")))
	}

	waitFor(t, func() bool { return len(handler.texts()) == chatBurst })

	// The one past the burst was answered, but not by the model.
	waitFor(t, func() bool {
		for _, c := range api.callsTo("sendMessage") {
			if text, _ := c.body["text"].(string); strings.Contains(text, "faster than I can think") {
				return true
			}
		}
		return false
	})

	if got := len(handler.texts()); got != chatBurst {
		t.Errorf("%d messages reached the model, want the burst of %d", got, chatBurst)
	}
}

func TestAnotherChatIsUnaffectedByABusyOne(t *testing.T) {
	limiter := newChatLimiter(rate.Every(time.Hour), 1)

	if !limiter.allow("4242") {
		t.Fatal("the first message from a chat was refused")
	}
	if limiter.allow("4242") {
		t.Error("the second message from the same chat was allowed")
	}
	// Per chat, not global, so one sender cannot starve everybody else.
	if !limiter.allow("9999") {
		t.Error("a different chat was refused because another was busy")
	}
}

func TestChatLimiterDoesNotGrowWithoutBound(t *testing.T) {
	// The key is a chat id, which is something a stranger can invent: an
	// uncapped map here is a way to run the pod out of memory from outside.
	limiter := newChatLimiter(rate.Every(time.Second), 1)
	for i := range chatLimiterEntries * 2 {
		limiter.allow(strconv.Itoa(i))
	}

	limiter.mu.Lock()
	size := len(limiter.limiters)
	limiter.mu.Unlock()

	if size > chatLimiterEntries {
		t.Errorf("holding %d limiters, past the %d cap", size, chatLimiterEntries)
	}
}

// A disabled limiter allows everything, which is what a nil one has to mean
// for the poller, where it is not configured.
func TestANilLimiterAllowsEverything(t *testing.T) {
	var limiter *chatLimiter
	for range 100 {
		if !limiter.allow("4242") {
			t.Fatal("a nil limiter refused a message")
		}
	}
	if newChatLimiter(0, 0) != nil {
		t.Error("a limiter with no budget should be nil, meaning disabled")
	}
}
