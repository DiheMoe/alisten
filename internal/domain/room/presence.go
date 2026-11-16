package room

import "slices"

func (r *Room) join(in Join) {
	if slices.ContainsFunc(r.sessions, func(s Session) bool { return s.ID == in.Session }) {
		return
	}
	r.sessions = append(r.sessions, Session{ID: in.Session, Member: in.Member})
	r.closeTimer = 0
}

func (r *Room) leave(b *batch, in Leave) {
	i := slices.IndexFunc(r.sessions, func(s Session) bool { return s.ID == in.Session })
	if i < 0 {
		return
	}
	r.sessions = slices.Delete(r.sessions, i, i+1)
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
