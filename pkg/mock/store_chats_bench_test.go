package mock

import (
	"sync/atomic"
	"testing"
)

// BenchmarkPollManyChats mirrors a k6 run: many chats receive bot replies
// while each virtual user polls only its own chat via MessagesSince.
func BenchmarkPollManyChats(b *testing.B) {
	const chats = 1000
	bot := NewStore().Bot("741:many-chats")
	for i := range 20 * chats {
		chat := int64(i%chats + 1)
		bot.AppendMessage(Message{MessageID: bot.parent.NextMessageID(), Chat: Chat{ID: chat}, Date: 1})
	}
	var n atomic.Int64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := n.Add(1)
			chat := i%chats + 1
			if i%4 == 0 {
				bot.AppendMessage(Message{MessageID: bot.parent.NextMessageID(), Chat: Chat{ID: chat}, Date: 2})
				continue
			}
			_ = bot.MessagesSince(chat, 2)
		}
	})
}
