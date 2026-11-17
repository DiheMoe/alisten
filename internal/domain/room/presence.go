package room

import "slices"

// 只限人数挡不住一个人开很多连接。每个连接都是一个会话，每次有人进出都要向每个会话广播完整的在线列表，所以连接数也要有上限。
const maxSessionsPerMember = 4

func (r *Room) join(b *batch, in Join) {
	if slices.ContainsFunc(r.sessions, func(s Session) bool { return s.ID == in.Session }) {
		if !in.Rejoin {
			b.respond(in.RequestID, Done{}, nil)
		}
		return
	}
	if !in.Rejoin {
		if err := r.admit(in.Member.ID); err != nil {
			b.respond(in.RequestID, nil, err)
			return
		}
		b.respond(in.RequestID, Done{}, nil)
	}
	r.sessions = append(r.sessions, Session{ID: in.Session, Member: in.Member})
	r.closeTimer = 0
}

// 上限限的是人数。已经有会话在房间里的成员再开连接，人数不变，所以不算新增人数，只受 maxSessionsPerMember 限制。
// 上限只在新会话加入时检查，比当前人数还低时也不踢人，因为被踢的都是正在听歌的人。这时新的成员进不来，直到人数降到上限以下。
func (r *Room) admit(memberID string) *Error {
	mine := 0
	for _, s := range r.sessions {
		if s.Member.ID == memberID {
			mine++
		}
	}
	if mine >= maxSessionsPerMember {
		return errTooManySessions
	}
	if mine == 0 && Headcount(r.sessions) >= r.info.Capacity {
		return errRoomFull(r.info.Capacity)
	}
	return nil
}

func (r *Room) leave(b *batch, in Leave) {
	i := slices.IndexFunc(r.sessions, func(s Session) bool { return s.ID == in.Session })
	if i < 0 {
		return
	}
	r.sessions = slices.Delete(r.sessions, i, i+1)
	r.recountVotes(b)
	if len(r.sessions) == 0 && r.info.Kind == Ephemeral {
		r.closeTimer = r.schedule(b, b.now.Add(closeDelay))
	}
}

// Headcount 是在线的人数。同一成员可能同时开着多个标签页或客户端，每个连接是一个会话，但只算一个人。
func Headcount(sessions []Session) int {
	people := map[string]bool{}
	for _, s := range sessions {
		people[s.Member.ID] = true
	}
	return len(people)
}

func (r *Room) memberUpdated(in MemberUpdated) {
	for i := range r.sessions {
		if r.sessions[i].Member.ID == in.Member.ID {
			r.sessions[i].Member = in.Member
		}
	}
}
