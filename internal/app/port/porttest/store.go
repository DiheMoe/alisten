// Package porttest 在内存中实现 internal/app/port 的端口，只供测试使用。
package porttest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
)

// Store 的行为与 postgres 适配器一致，包括它会拒绝的输入。
type Store struct {
	mu      sync.Mutex
	ids     int
	members []storedMember
	rooms   []port.RoomRecord
	states  map[string]*roomState
	tickets map[string]storedTicket
	// 房间 ID 到 bot 成员 ID。
	bots      map[string]string
	cookies   map[string]string
	brandName string
}

type storedMember struct {
	tokenHash string
	member    member.Member
}

type storedTicket struct {
	roomID    string
	memberID  string
	createdAt time.Time
}

var (
	_ port.Members       = (*Store)(nil)
	_ port.Tickets       = (*Store)(nil)
	_ port.RoomDirectory = (*Store)(nil)
	_ port.Credentials   = (*Store)(nil)
	_ port.APIKeys       = (*Store)(nil)
	_ port.Site          = (*Store)(nil)
)

func New() *Store {
	return &Store{
		states:  map[string]*roomState{},
		tickets: map[string]storedTicket{},
		bots:    map[string]string{},
		cookies: map[string]string{},
	}
}

func (s *Store) newID() string {
	s.ids++
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", s.ids)
}

var uuidPattern = regexp.MustCompile(`^(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// PostgreSQL 的 text 无法存储 U+0000，也不接受不合法的 UTF-8，查询参数中出现时直接报错。
func checkText(values ...string) error {
	for _, v := range values {
		if strings.ContainsRune(v, 0) || !utf8.ValidString(v) {
			return fmt.Errorf("PostgreSQL 无法处理文本 %q", v)
		}
	}
	return nil
}

func (s *Store) CreateMember(_ context.Context, tokenHash []byte, name string) (member.Member, error) {
	if err := checkText(name); err != nil {
		return member.Member{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.members {
		if m.tokenHash == string(tokenHash) {
			return member.Member{}, errors.New("token 哈希重复")
		}
	}
	m := member.Member{ID: s.newID(), Name: name}
	s.members = append(s.members, storedMember{tokenHash: string(tokenHash), member: m})
	return m, nil
}

func (s *Store) MemberByToken(_ context.Context, tokenHash []byte) (member.Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.members {
		if m.tokenHash == string(tokenHash) {
			return m.member, nil
		}
	}
	return member.Member{}, port.ErrMemberNotFound
}

func (s *Store) UpdateMember(_ context.Context, id string, name, avatarHash *string) (member.Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.members {
		m := &s.members[i].member
		if m.ID != id {
			continue
		}
		if name != nil {
			if err := checkText(*name); err != nil {
				return member.Member{}, err
			}
			m.Name = *name
		}
		if avatarHash != nil {
			m.AvatarHash = *avatarHash
		}
		return *m, nil
	}
	return member.Member{}, port.ErrMemberNotFound
}

func (s *Store) CreateTicket(_ context.Context, t port.NewTicket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.room(t.RoomID)
	if r == nil {
		return port.ErrRoomNotFound
	}
	if r.PasswordHash != t.PasswordHash {
		return port.ErrPasswordChanged
	}
	if !slices.ContainsFunc(s.members, func(m storedMember) bool { return m.member.ID == t.MemberID }) {
		return errors.New("成员不存在")
	}
	if _, ok := s.tickets[string(t.TokenHash)]; ok {
		return errors.New("票据哈希重复")
	}
	s.tickets[string(t.TokenHash)] = storedTicket{roomID: r.Info.ID, memberID: t.MemberID, createdAt: time.Now()}
	return nil
}

func (s *Store) TicketHolder(_ context.Context, tokenHash []byte) (port.TicketHolder, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[string(tokenHash)]
	if !ok {
		return port.TicketHolder{}, port.ErrTicketNotFound
	}
	for _, m := range s.members {
		if m.member.ID == t.memberID {
			return port.TicketHolder{RoomID: t.roomID, Member: m.member, Bot: s.bots[t.roomID] == t.memberID}, nil
		}
	}
	return port.TicketHolder{}, port.ErrTicketNotFound
}

func (s *Store) RoomAPIKeyStatus(_ context.Context, roomID string) (port.APIKeyStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.room(roomID)
	if r == nil {
		return port.APIKeyStatus{}, nil
	}
	if bot, ok := s.bots[r.Info.ID]; ok {
		for _, t := range s.tickets {
			if t.roomID == r.Info.ID && t.memberID == bot {
				return port.APIKeyStatus{Set: true, CreatedAt: t.createdAt}, nil
			}
		}
	}
	return port.APIKeyStatus{}, nil
}

func (s *Store) ResetRoomAPIKey(_ context.Context, roomID string, k port.NewAPIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.room(roomID)
	if r == nil {
		return port.ErrRoomNotFound
	}
	if r.Info.Kind != room.Persistent {
		return errors.New("只有常驻房间可以有 bot 成员")
	}
	bot, ok := s.bots[r.Info.ID]
	if ok {
		maps.DeleteFunc(s.tickets, func(_ string, t storedTicket) bool { return t.roomID == r.Info.ID && t.memberID == bot })
	} else {
		bot = s.newID()
		s.members = append(s.members, storedMember{tokenHash: string(k.BotTokenHash), member: member.Member{ID: bot, Name: "bot"}})
		s.bots[r.Info.ID] = bot
	}
	s.tickets[string(k.TokenHash)] = storedTicket{roomID: r.Info.ID, memberID: bot, createdAt: time.Now()}
	return nil
}

func (s *Store) CreateRoom(_ context.Context, r port.NewRoom) (string, error) {
	if err := checkText(r.Name, r.Description, r.Slug, r.PasswordHash); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Slug != "" && r.Kind != room.Persistent {
		return "", errors.New("只有常驻房间可以有别名")
	}
	if r.Slug != "" && s.slugOwner(r.Slug) != nil {
		return "", port.ErrSlugTaken
	}
	id := s.newID()
	s.rooms = append(s.rooms, port.RoomRecord{
		Info:         room.Info{ID: id, Name: r.Name, Description: r.Description, Kind: r.Kind},
		Slug:         r.Slug,
		PasswordHash: r.PasswordHash,
	})
	return id, nil
}

func (s *Store) ListRooms(context.Context) ([]port.RoomRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.rooms), nil
}

func (s *Store) FindRoom(_ context.Context, ref string) (port.RoomRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r *port.RoomRecord
	if uuidPattern.MatchString(ref) {
		r = s.room(ref)
	} else {
		if err := checkText(ref); err != nil {
			return port.RoomRecord{}, err
		}
		r = s.slugOwner(ref)
	}
	if r == nil {
		return port.RoomRecord{}, fmt.Errorf("查找房间 %s：%w", ref, port.ErrRoomNotFound)
	}
	return *r, nil
}

func (s *Store) UpdateRoomAccess(_ context.Context, id string, c port.AccessChange) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.room(id)
	if r == nil {
		return port.ErrRoomNotFound
	}
	updated := *r
	if c.Slug != nil {
		if err := checkText(*c.Slug); err != nil {
			return err
		}
		if owner := s.slugOwner(*c.Slug); *c.Slug != "" && owner != nil && owner.Info.ID != r.Info.ID {
			return port.ErrSlugTaken
		}
		if *c.Slug != "" && r.Info.Kind != room.Persistent {
			return errors.New("只有常驻房间可以有别名")
		}
		updated.Slug = *c.Slug
	}
	if c.PasswordHash != nil {
		if err := checkText(*c.PasswordHash); err != nil {
			return err
		}
		updated.PasswordHash = *c.PasswordHash
		// 取消密码时，已经签发的票据保持有效。
		if *c.PasswordHash != "" {
			bot := s.bots[r.Info.ID]
			maps.DeleteFunc(s.tickets, func(_ string, t storedTicket) bool { return t.roomID == r.Info.ID && t.memberID != bot })
		}
	}
	*r = updated
	return nil
}

func (s *Store) NeteaseCookie(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cookies["netease"], nil
}

func (s *Store) SetNeteaseCookie(_ context.Context, cookie string) error {
	if err := checkText(cookie); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cookies["netease"] = cookie
	return nil
}

func (s *Store) QQCookie(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cookies["qq"], nil
}

func (s *Store) SetQQCookie(_ context.Context, cookie string) error {
	if err := checkText(cookie); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cookies["qq"] = cookie
	return nil
}

func (s *Store) BilibiliCredential(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cookies["bilibili"], nil
}

func (s *Store) SetBilibiliCredential(_ context.Context, credential string) error {
	if err := checkText(credential); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cookies["bilibili"] = credential
	return nil
}

func (s *Store) BrandName(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.brandName, nil
}

func (s *Store) SetBrandName(_ context.Context, name string) error {
	if err := checkText(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.brandName = name
	return nil
}

func (s *Store) room(id string) *port.RoomRecord {
	for i := range s.rooms {
		if strings.EqualFold(s.rooms[i].Info.ID, id) {
			return &s.rooms[i]
		}
	}
	return nil
}

func (s *Store) slugOwner(slug string) *port.RoomRecord {
	for i := range s.rooms {
		if s.rooms[i].Slug != "" && s.rooms[i].Slug == slug {
			return &s.rooms[i]
		}
	}
	return nil
}
