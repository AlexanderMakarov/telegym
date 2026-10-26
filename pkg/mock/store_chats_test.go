package mock

import (
	"math"
	"sync"
	"testing"
)

func TestMessageIDsArePerChatAndFitInt32(t *testing.T) {
	s := NewStore()
	b := s.Bot("741:ids")
	base := s.messageIDBase
	if base < 0 || base >= 1_000_000_000 {
		t.Fatalf("message_id base out of range: %d", base)
	}
	// Each chat has its own sequence starting right after the base.
	if a1, b1, a2 := b.NextMessageID(1), b.NextMessageID(2), b.NextMessageID(1); a1 != int(base)+1 || b1 != int(base)+1 || a2 != a1+1 {
		t.Fatalf("per-chat sequences: chat1=%d chat2=%d chat1 again=%d (base %d)", a1, b1, a2, base)
	}
	for range 100_000 {
		if id := b.NextMessageID(3); id <= 0 || id > math.MaxInt32 {
			t.Fatalf("message_id out of int32 range: %d", id)
		}
	}
	if id := s.NextMessageID(); id <= 0 || id > math.MaxInt32 {
		t.Fatalf("legacy NextMessageID out of int32 range: %d", id)
	}
}

func TestForumTopicThreadUsesChatSequence(t *testing.T) {
	b := NewStore().Bot("741:topics")
	before := b.NextMessageID(5)
	topic := b.CreateForumTopic(5, "news", 0, "")
	if topic.MessageThreadID != before+1 {
		t.Fatalf("thread id %d, want next message_id %d", topic.MessageThreadID, before+1)
	}
	if next := b.NextMessageID(5); next != topic.MessageThreadID+1 {
		t.Fatalf("message after topic got %d, want %d", next, topic.MessageThreadID+1)
	}
}

// TestChatLogOrderedByEventSeq guards the xk6 dedupe contract: a poller skips
// sequences it has seen, so a chat log must never hold a lower EventSeq after
// a higher one, even with concurrent writers to the same chat.
func TestChatLogOrderedByEventSeq(t *testing.T) {
	b := NewStore().Bot("741:order")
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				b.AppendMessage(Message{MessageID: b.NextMessageID(7), Chat: Chat{ID: 7}})
			}
		}()
	}
	wg.Wait()
	msgs := b.MessagesSince(7, 0)
	if len(msgs) != DefaultMaxMessagesPerChat {
		t.Fatalf("got %d messages", len(msgs))
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].EventSeq <= msgs[i-1].EventSeq {
			t.Fatalf("log out of order at %d: %d after %d", i, msgs[i].EventSeq, msgs[i-1].EventSeq)
		}
	}
}

func TestBusyChatDoesNotEvictOthers(t *testing.T) {
	b := NewStore().Bot("741:isolation")
	b.AppendMessage(Message{MessageID: b.NextMessageID(1), Chat: Chat{ID: 1}, Text: "quiet"})
	for range 10 * DefaultMaxMessagesPerChat {
		b.AppendMessage(Message{MessageID: b.NextMessageID(2), Chat: Chat{ID: 2}})
	}
	if msgs := b.Messages(1, 10); len(msgs) != 1 || msgs[0].Text != "quiet" {
		t.Fatalf("busy chat evicted another chat's history: %+v", msgs)
	}
	if msgs, chats := b.messageStats(); msgs != 1+DefaultMaxMessagesPerChat || chats != 2 {
		t.Fatalf("stats: messages=%d chats=%d", msgs, chats)
	}
}

func TestMessagesAcrossChatsNewestFirst(t *testing.T) {
	b := NewStore().Bot("741:all")
	for i := range 6 {
		chat := int64(i%3 + 1)
		b.AppendMessage(Message{MessageID: b.NextMessageID(chat), Chat: Chat{ID: chat}, Date: int64(i)})
	}
	all := b.Messages(0, 4)
	if len(all) != 4 {
		t.Fatalf("got %d messages", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i].EventSeq >= all[i-1].EventSeq {
			t.Fatalf("not newest first: %+v", all)
		}
	}
	if since := b.MessagesSince(0, 3); len(since) != 3 || since[0].Date != 3 || since[2].Date != 5 {
		t.Fatalf("MessagesSince across chats: %+v", since)
	}
	if got := b.Messages(99, 10); len(got) != 0 {
		t.Fatalf("unknown chat returned messages: %+v", got)
	}
	if b.existingChat(99) != nil {
		t.Fatal("reading an unknown chat allocated a log")
	}
}

func TestClearMessagesKeepsIDSequence(t *testing.T) {
	b := NewStore().Bot("741:clear")
	last := b.NextMessageID(1)
	b.AppendMessage(Message{MessageID: last, Chat: Chat{ID: 1}})
	if n := b.ClearMessages(); n != 1 {
		t.Fatalf("cleared %d", n)
	}
	if next := b.NextMessageID(1); next != last+1 {
		t.Fatalf("message_id restarted after clear: %d, want %d", next, last+1)
	}
}

func TestToastQueueCapped(t *testing.T) {
	b := NewStore().Bot("741:toasts")
	for i := range 3 * maxToastsPerChat {
		b.PushToast(1, Toast{Text: string(rune('a' + i%26)), At: int64(i)})
	}
	first, ok := b.PopToast(1)
	if !ok || first.At != 2*maxToastsPerChat {
		t.Fatalf("oldest kept toast %+v, want At=%d", first, 2*maxToastsPerChat)
	}
	n := 1
	for {
		if _, ok := b.PopToast(1); !ok {
			break
		}
		n++
	}
	if n != maxToastsPerChat {
		t.Fatalf("kept %d toasts, want %d", n, maxToastsPerChat)
	}
}
