package port

import (
	"context"

	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
)

// owner 按产生的顺序发布，订阅的网关按同样的顺序收到。
type RoomMessage interface{ roomMessage() }

type (
	// 房间开始了新的一次运行：在线列表为空，事件重新编号。各网关要为本地会话重新发送 Join，为事件流重新获取快照。
	OwnerChanged struct {
		Term uint32
	}

	Emitted struct {
		Emit room.Emit
	}

	Replied struct {
		Reply room.Reply
	}

	// 请求没有被房间处理，之后也不会有回复。
	Rejected struct {
		RequestID room.RequestID
		// 为真时房间不存在，否则是房间暂时不可用。
		NotFound bool
	}

	// 房间的数据已经删除，它的会话和事件流要以 4404 断开。
	RoomClosed struct{}

	OwnerReleased struct{}

	// 房间成员的票据已经作废，会话和成员打开的事件流要以 4401 断开，用 API Key 打开的事件流不受影响。由管理接口发布。
	TicketsRevoked struct{}

	// 常驻房间的 API Key 已经重置，用 Key 打开的事件流要以 4401 断开，成员的会话和事件流不受影响。由管理接口发布。
	APIKeyReset struct{}
)

func (OwnerChanged) roomMessage()   {}
func (Emitted) roomMessage()        {}
func (Replied) roomMessage()        {}
func (Rejected) roomMessage()       {}
func (RoomClosed) roomMessage()     {}
func (OwnerReleased) roomMessage()  {}
func (TicketsRevoked) roomMessage() {}
func (APIKeyReset) roomMessage()    {}

type RoomInput struct {
	Room string
	// 发出输入的网关的实例 ID：owner 据此记录会话所在的网关。
	Gateway string
	// Input 与 Patch 恰好有一项不为空。
	Input room.Input
	Patch *InfoPatch
	// 为真时，如果接收输入的实例上没有运行这个房间，就由它接管。
	Takeover bool
}

// nil 表示保持不变：管理接口只修改给出的字段，由房间按当前的名称和描述补全为 UpdateInfo。
type InfoPatch struct {
	RequestID   room.RequestID
	Name        *string
	Description *string
}

type Delivery interface{ delivery() }

type (
	RoomDelivery struct {
		Room    string
		Message RoomMessage
	}

	InputDelivery struct {
		// 带有网关一侧的追踪上下文，owner 在同一条 trace 中处理这条输入；网关一侧没有追踪时为 context.Background()。
		Context context.Context
		Input   RoomInput
	}

	MemberDelivery struct {
		Member member.Member
	}

	PreparedDelivery struct {
		Outcome PrepareOutcome
	}

	// 订阅连接曾经断开，断开期间发给本实例的消息都已丢失。
	SubscriptionLost struct{}
)

func (RoomDelivery) delivery()     {}
func (InputDelivery) delivery()    {}
func (MemberDelivery) delivery()   {}
func (PreparedDelivery) delivery() {}
func (SubscriptionLost) delivery() {}

// 实现绑定在本实例上：订阅的是本实例的频道，发出的消息带有本实例的 ID。
type Bus interface {
	// 订阅本实例的频道、成员资料的频道和准备结果的频道，返回时订阅已经生效。
	SubscribeInstance(ctx context.Context) error
	// 返回时订阅已经生效：此后发布到这个房间频道的消息都会收到。
	SubscribeRoom(ctx context.Context, roomID string) error
	UnsubscribeRoom(ctx context.Context, roomID string) error
	PublishRoom(ctx context.Context, roomID string, m RoomMessage) error
	// 返回 false 表示目标实例没有订阅它的频道，例如已经停止运行。
	SendInput(ctx context.Context, instanceID string, in RoomInput) (bool, error)
	PublishMember(ctx context.Context, m member.Member) error
	// 各实例都会收到，包括发布的实例。
	PublishPrepared(ctx context.Context, o PrepareOutcome) error
	// 按收到的顺序返回下一条消息，直到 ctx 结束；ctx 结束之后 Bus 不能再使用。只能由一个 goroutine 调用。
	Receive(ctx context.Context) (Delivery, error)
}
