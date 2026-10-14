package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/Fugguri/tgvault/internal/approve"
	"github.com/Fugguri/tgvault/internal/tgx"
)

type forwardOpts struct {
	from, dest             string
	fromID, destID         int64
	fromAlias, destAlias   string
	self                   bool
	destTopic              int
	msgs                   []int
	last                   int
	dropAuthor, silent, do bool
}

func manageForward(ctx context.Context, client *telegram.Client, dir string, o forwardOpts) error {
	src, err := resolveChatTarget(ctx, client, dir, o.from, o.fromID, o.fromAlias, o.self)
	if err != nil {
		return fmt.Errorf("источник: %w", err)
	}
	dst, err := resolveChatTarget(ctx, client, dir, o.dest, o.destID, o.destAlias, false)
	if err != nil {
		return fmt.Errorf("назначение: %w", err)
	}

	ids := o.msgs
	if len(ids) == 0 && o.last > 0 {
		msgs, err := tgx.History(ctx, client.API(), src.Peer, o.last)
		if err != nil {
			return err
		}
		for i := len(msgs) - 1; i >= 0; i-- {
			if m, ok := msgs[i].(*tg.Message); ok {
				ids = append(ids, m.ID)
			}
		}
	}
	if len(ids) == 0 {
		return fmt.Errorf("forward: нужны -msg ID… или -last N")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "FORWARD\nОткуда:  %s\nКуда:    %s\n", src.Desc, dst.Desc)
	if o.destTopic > 0 {
		fmt.Fprintf(&b, "Топик:   %d\n", o.destTopic)
	}
	fmt.Fprintf(&b, "Сообщения: %v\n", ids)
	if o.dropAuthor {
		b.WriteString("Автор:   скрыть\n")
	}
	if o.silent {
		b.WriteString("Тихо:    да\n")
	}
	payload := approve.Canon("manage/forward", peerKey(src.Peer), peerKey(dst.Peer),
		intsKey(ids), strconv.Itoa(o.destTopic), fmt.Sprint(o.dropAuthor), fmt.Sprint(o.silent))
	if err := Gate(o.do, "manage/forward", b.String(), payload); err != nil {
		return err
	}
	if !o.do {
		return nil
	}

	req := &tg.MessagesForwardMessagesRequest{
		FromPeer: src.Peer, ToPeer: dst.Peer, ID: ids, RandomID: randomIDs(len(ids)),
		DropAuthor: o.dropAuthor, Silent: o.silent,
	}
	if o.destTopic > 0 {
		req.SetTopMsgID(o.destTopic)
	}
	if _, err := client.API().MessagesForwardMessages(ctx, req); err != nil {
		return err
	}
	fmt.Printf("✅ переслано сообщений: %d (%s → %s)\n", len(ids), src.Desc, dst.Desc)
	return nil
}

func randomIDs(n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = randomID()
	}
	return out
}
