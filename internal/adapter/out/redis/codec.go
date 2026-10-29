package redis

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
)

// 只有同一构建版本的实例互相通信，格式不需要跨版本兼容。
type envelope struct {
	Version string
	From    string
	Kind    string
	Trace   map[string]string `json:",omitempty"`
	Data    json.RawMessage
}

type (
	wireEmit struct {
		Term  uint32
		Seq   uint64
		Event string
		Data  json.RawMessage
	}

	wireReply struct {
		RequestID room.RequestID
		Result    string
		Data      json.RawMessage
		Err       *room.Error
	}

	wireInput struct {
		Room     string
		Gateway  string
		Takeover bool
		Input    string
		Data     json.RawMessage
		Patch    *port.InfoPatch
	}

	wireCommand struct {
		RequestID room.RequestID
		Member    member.Member
		Action    string
		Data      json.RawMessage
	}
)

const (
	kindEmitted  = "Emitted"
	kindReplied  = "Replied"
	kindInput    = "RoomInput"
	kindMember   = "Member"
	kindPrepared = "Prepared"
	// room.Command 的 Action 是接口，不能像 inputs 中的类型那样直接编码。
	inputCommand = "Command"
)

// 接口实现必须登记后才能编码；实现字段中不能再有接口类型。
type union[T any] map[string]reflect.Type

func newUnion[T any](variants ...T) union[T] {
	u := union[T]{}
	for _, v := range variants {
		t := reflect.TypeOf(v)
		u[t.Name()] = t
	}
	return u
}

func (u union[T]) encode(v T) (string, json.RawMessage, error) {
	t := reflect.TypeOf(v)
	if t == nil || u[t.Name()] != t {
		return "", nil, fmt.Errorf("不能编码为实例间消息的类型 %T", v)
	}
	data, err := json.Marshal(v)
	return t.Name(), data, err
}

func (u union[T]) decode(name string, data json.RawMessage) (T, error) {
	var zero T
	t, ok := u[name]
	if !ok {
		return zero, fmt.Errorf("未知的类型 %q", name)
	}
	p := reflect.New(t)
	if err := json.Unmarshal(data, p.Interface()); err != nil {
		return zero, err
	}
	return p.Elem().Interface().(T), nil
}

var (
	roomMessages = newUnion[port.RoomMessage](port.OwnerChanged{}, port.Rejected{}, port.RoomClosed{}, port.OwnerReleased{}, port.TicketsRevoked{}, port.APIKeyReset{})
	events       = newUnion[room.Event](room.PlaybackChanged{}, room.QueueChanged{}, room.MembersChanged{}, room.ModeChanged{}, room.InfoChanged{}, room.ChatPosted{}, room.Activity{})
	results      = newUnion[room.Result](room.Done{}, room.EnqueueResult{}, room.LikeResult{}, room.VoteResult{}, room.SyncResult{})
	inputs       = newUnion[room.Input](room.Join{}, room.Leave{}, room.MemberUpdated{}, room.Delete{})
	actions      = newUnion[room.Action](room.Enqueue{}, room.Remove{}, room.Like{}, room.VoteSkip{}, room.SetPlayMode{}, room.Chat{}, room.Sync{})
)

func encodeRoomMessage(m port.RoomMessage) (string, json.RawMessage, error) {
	switch m := m.(type) {
	case port.Emitted:
		w := wireEmit{Term: m.Emit.Term, Seq: m.Emit.Seq}
		var err error
		if w.Event, w.Data, err = events.encode(m.Emit.Event); err != nil {
			return "", nil, err
		}
		data, err := json.Marshal(w)
		return kindEmitted, data, err
	case port.Replied:
		w := wireReply{RequestID: m.Reply.RequestID, Err: m.Reply.Err}
		if m.Reply.Err == nil {
			var err error
			if w.Result, w.Data, err = results.encode(m.Reply.Result); err != nil {
				return "", nil, err
			}
		}
		data, err := json.Marshal(w)
		return kindReplied, data, err
	}
	return roomMessages.encode(m)
}

func decodeRoomMessage(kind string, data json.RawMessage) (port.RoomMessage, error) {
	switch kind {
	case kindEmitted:
		var w wireEmit
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		e, err := events.decode(w.Event, w.Data)
		if err != nil {
			return nil, err
		}
		return port.Emitted{Emit: room.Emit{Term: w.Term, Seq: w.Seq, Event: e}}, nil
	case kindReplied:
		var w wireReply
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		reply := room.Reply{RequestID: w.RequestID, Err: w.Err}
		if w.Err == nil {
			res, err := results.decode(w.Result, w.Data)
			if err != nil {
				return nil, err
			}
			reply.Result = res
		}
		return port.Replied{Reply: reply}, nil
	}
	return roomMessages.decode(kind, data)
}

var errEmptyInput = errors.New("房间输入没有内容")

func encodeInput(in port.RoomInput) (json.RawMessage, error) {
	w := wireInput{Room: in.Room, Gateway: in.Gateway, Takeover: in.Takeover, Patch: in.Patch}
	switch i := in.Input.(type) {
	case nil:
		if in.Patch == nil {
			return nil, errEmptyInput
		}
	case room.Command:
		c := wireCommand{RequestID: i.RequestID, Member: i.Member}
		var err error
		if c.Action, c.Data, err = actions.encode(i.Action); err != nil {
			return nil, err
		}
		w.Input = inputCommand
		if w.Data, err = json.Marshal(c); err != nil {
			return nil, err
		}
	default:
		var err error
		if w.Input, w.Data, err = inputs.encode(i); err != nil {
			return nil, err
		}
	}
	return json.Marshal(w)
}

func decodeInput(data json.RawMessage) (port.RoomInput, error) {
	var w wireInput
	if err := json.Unmarshal(data, &w); err != nil {
		return port.RoomInput{}, err
	}
	in := port.RoomInput{Room: w.Room, Gateway: w.Gateway, Takeover: w.Takeover, Patch: w.Patch}
	switch w.Input {
	case "":
		if w.Patch == nil {
			return port.RoomInput{}, errEmptyInput
		}
	case inputCommand:
		var c wireCommand
		if err := json.Unmarshal(w.Data, &c); err != nil {
			return port.RoomInput{}, err
		}
		a, err := actions.decode(c.Action, c.Data)
		if err != nil {
			return port.RoomInput{}, err
		}
		in.Input = room.Command{RequestID: c.RequestID, Member: c.Member, Action: a}
	default:
		i, err := inputs.decode(w.Input, w.Data)
		if err != nil {
			return port.RoomInput{}, err
		}
		in.Input = i
	}
	return in, nil
}
