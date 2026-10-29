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

func (r *Room) memberUpdated(in MemberUpdated) {
	for i := range r.sessions {
		if r.sessions[i].Member.ID == in.Member.ID {
			r.sessions[i].Member = in.Member
		}
	}
}
