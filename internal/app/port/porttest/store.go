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
	"github.com/dihemoe/alisten/internal/domain/playlist"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

// Store 的行为与 postgres 适配器一致，包括它会拒绝的输入。
type Store struct {
	mu      sync.Mutex
	ids     int
	members []storedMember
	// 会话的 token 哈希到成员 ID。
	sessions map[string]string
	rooms    []port.RoomRecord
	states   map[string]*roomState
	tickets  map[string]storedTicket
	// 房间 ID 到 bot 成员 ID。
	bots               map[string]string
	cookies            map[string]string
	githubApp          port.GitHubOAuthApp
	brandName          string
	registrationClosed bool
	playlists          []*storedPlaylist
	// 成员 ID 到播放历史，按 MemberListens 的顺序排列。
	listens map[string][]port.Listen
}

type storedMember struct {
	member       member.Member
	username     string
	email        string
	passwordHash string
	githubID     int64
	// 零值表示记录播放历史，与 members.record_listens 的默认值一致。
	listensOff bool
}

func (m *storedMember) registered() bool {
	return m.username != "" || m.githubID != 0
}

func (m *storedMember) account() port.Account {
	return port.Account{Member: m.member, Username: m.username, Email: m.email, PasswordHash: m.passwordHash, GitHubID: m.githubID}
}

// 调用方持有 s.mu。
func (s *Store) createFavorites(memberID string) {
	now := time.Now()
	s.playlists = append(s.playlists, &storedPlaylist{id: s.newID(), owner: memberID, name: "我喜欢", favorites: true, created: now, updated: now})
}

type storedTicket struct {
	roomID    string
	memberID  string
	session   string
	createdAt time.Time
}

var (
	_ port.Members         = (*Store)(nil)
	_ port.Tickets         = (*Store)(nil)
	_ port.ExistingTickets = (*Store)(nil)
	_ port.RoomDirectory   = (*Store)(nil)
	_ port.Credentials     = (*Store)(nil)
	_ port.GitHubOAuthApps = (*Store)(nil)
	_ port.APIKeys         = (*Store)(nil)
	_ port.Site            = (*Store)(nil)
	_ port.Playlists       = (*Store)(nil)
)

func New() *Store {
	return &Store{
		states:   map[string]*roomState{},
		sessions: map[string]string{},
		tickets:  map[string]storedTicket{},
		bots:     map[string]string{},
		cookies:  map[string]string{},
		listens:  map[string][]port.Listen{},
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
	if _, ok := s.sessions[string(tokenHash)]; ok {
		return member.Member{}, errors.New("token 哈希重复")
	}
	m := member.Member{ID: s.newID(), Name: name}
	s.members = append(s.members, storedMember{member: m})
	s.sessions[string(tokenHash)] = m.ID
	return m, nil
}

func (s *Store) MemberByToken(_ context.Context, tokenHash []byte) (member.Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.member(s.sessions[string(tokenHash)]); m != nil {
		return m.member, nil
	}
	return member.Member{}, port.ErrMemberNotFound
}

// 调用方持有 s.mu。
func (s *Store) member(id string) *storedMember {
	for i := range s.members {
		if s.members[i].member.ID == id {
			return &s.members[i]
		}
	}
	return nil
}

func (s *Store) UpdateMember(_ context.Context, id string, name, avatarHash, email *string) (member.Member, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.members {
		stored := &s.members[i]
		m := &stored.member
		if m.ID != id {
			continue
		}
		if email != nil {
			if err := checkText(*email); err != nil {
				return member.Member{}, err
			}
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
		if email != nil && stored.registered() {
			stored.email = *email
		}
		return *m, nil
	}
	return member.Member{}, port.ErrMemberNotFound
}

func (s *Store) AccountProfile(_ context.Context, memberID string) (port.AccountProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.member(memberID)
	if m == nil {
		return port.AccountProfile{}, port.ErrMemberNotFound
	}
	return port.AccountProfile{Username: m.username, Email: m.email, GitHub: m.githubID != 0}, nil
}

func (s *Store) Register(_ context.Context, memberID, username, passwordHash string) error {
	if err := checkText(username, passwordHash); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.member(memberID)
	if m == nil || m.registered() {
		return port.ErrAlreadyRegistered
	}
	if slices.ContainsFunc(s.members, func(other storedMember) bool { return other.username == username }) {
		return port.ErrUsernameTaken
	}
	s.createFavorites(memberID)
	m.username, m.passwordHash = username, passwordHash
	return nil
}

func (s *Store) AddCredentials(_ context.Context, memberID, username, passwordHash string, keepTokenHash []byte) ([][]byte, error) {
	if err := checkText(username, passwordHash); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.member(memberID)
	if m == nil || m.username != "" || m.githubID == 0 {
		return nil, port.ErrAlreadyRegistered
	}
	if slices.ContainsFunc(s.members, func(other storedMember) bool { return other.username == username }) {
		return nil, port.ErrUsernameTaken
	}
	m.username, m.passwordHash = username, passwordHash
	return s.deleteSessions(memberID, keepTokenHash), nil
}

func (s *Store) AccountByUsername(_ context.Context, username string) (port.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.members {
		if username != "" && m.username == username {
			return m.account(), nil
		}
	}
	return port.Account{}, port.ErrMemberNotFound
}

func (s *Store) AccountByID(_ context.Context, memberID string) (port.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.member(memberID)
	if m == nil || !m.registered() {
		return port.Account{}, port.ErrMemberNotFound
	}
	return m.account(), nil
}

func (s *Store) AccountByGitHub(_ context.Context, githubID int64) (port.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.members {
		if m.githubID == githubID {
			return m.account(), nil
		}
	}
	return port.Account{}, port.ErrMemberNotFound
}

func (s *Store) BindGitHub(_ context.Context, memberID string, githubID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.member(memberID)
	if m == nil || m.githubID != 0 {
		return port.ErrGitHubAlreadyBound
	}
	if slices.ContainsFunc(s.members, func(other storedMember) bool { return other.githubID == githubID }) {
		return port.ErrGitHubTaken
	}
	if !m.registered() {
		s.createFavorites(memberID)
	}
	m.githubID = githubID
	return nil
}

func (s *Store) UnbindGitHub(_ context.Context, memberID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.member(memberID)
	// 密码总是和用户名一起设置：没有用户名，就是 PostgreSQL 中 password_hash 为 NULL。
	if m == nil || m.username == "" {
		return port.ErrNoPassword
	}
	m.githubID = 0
	return nil
}

func (s *Store) ReplaceSession(_ context.Context, oldTokenHash []byte, memberID string, newTokenHash []byte, passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.member(memberID)
	if m == nil {
		return fmt.Errorf("成员 %s 不存在", memberID)
	}
	if m.passwordHash != passwordHash {
		return port.ErrAccountPasswordChanged
	}
	previous, ok := s.sessions[string(oldTokenHash)]
	if !ok {
		return port.ErrSessionNotFound
	}
	if _, ok := s.sessions[string(newTokenHash)]; ok {
		return errors.New("token 哈希重复")
	}
	s.deleteSession(string(oldTokenHash))
	s.sessions[string(newTokenHash)] = memberID
	if previous != memberID {
		moved := s.listens[previous]
		delete(s.listens, previous)
		if !m.listensOff {
			for _, l := range moved {
				s.addListen(memberID, l)
			}
		}
	}
	return nil
}

func (s *Store) DeleteSession(_ context.Context, tokenHash []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteSession(string(tokenHash))
	return nil
}

func (s *Store) SetPassword(_ context.Context, memberID, passwordHash string) ([][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.member(memberID)
	if m == nil || m.username == "" {
		return nil, port.ErrMemberNotFound
	}
	m.passwordHash = passwordHash
	return s.deleteSessions(memberID, nil), nil
}

func (s *Store) ChangePassword(_ context.Context, memberID, currentPasswordHash, passwordHash string, keepTokenHash []byte) ([][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.member(memberID)
	if m == nil || m.username == "" {
		return nil, port.ErrMemberNotFound
	}
	if owner, ok := s.sessions[string(keepTokenHash)]; !ok || owner != memberID {
		return nil, port.ErrSessionNotFound
	}
	if m.passwordHash != currentPasswordHash {
		return nil, port.ErrAccountPasswordChanged
	}
	m.passwordHash = passwordHash
	return s.deleteSessions(memberID, keepTokenHash), nil
}

// 调用方持有 s.mu。
func (s *Store) deleteSessions(memberID string, keepTokenHash []byte) [][]byte {
	var deleted [][]byte
	for hash, id := range s.sessions {
		if id == memberID && (keepTokenHash == nil || hash != string(keepTokenHash)) {
			deleted = append(deleted, []byte(hash))
		}
	}
	for _, hash := range deleted {
		s.deleteSession(string(hash))
	}
	return deleted
}

// 与 postgres 的外键一致：会话的票据随会话一起删除。调用方持有 s.mu。
func (s *Store) deleteSession(hash string) {
	delete(s.sessions, hash)
	maps.DeleteFunc(s.tickets, func(_ string, t storedTicket) bool { return t.session == hash })
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
	if _, ok := s.sessions[string(t.SessionTokenHash)]; t.SessionTokenHash != nil && !ok {
		return port.ErrSessionNotFound
	}
	if _, ok := s.tickets[string(t.TokenHash)]; ok {
		return errors.New("票据哈希重复")
	}
	s.tickets[string(t.TokenHash)] = storedTicket{roomID: r.Info.ID, memberID: t.MemberID, session: string(t.SessionTokenHash), createdAt: time.Now()}
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
			holder := port.TicketHolder{RoomID: t.roomID, Member: m.member, Bot: s.bots[t.roomID] == t.memberID}
			if t.session != "" {
				holder.SessionTokenHash = []byte(t.session)
			}
			return holder, nil
		}
	}
	return port.TicketHolder{}, port.ErrTicketNotFound
}

func (s *Store) ExistingTickets(_ context.Context, tokenHashes [][]byte) ([][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var existing [][]byte
	for hash := range s.tickets {
		if slices.ContainsFunc(tokenHashes, func(h []byte) bool { return string(h) == hash }) {
			existing = append(existing, []byte(hash))
		}
	}
	return existing, nil
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
		s.members = append(s.members, storedMember{member: member.Member{ID: bot, Name: "bot"}})
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

func (s *Store) GitHubOAuthApp(context.Context) (port.GitHubOAuthApp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.githubApp, nil
}

func (s *Store) SetGitHubOAuthApp(_ context.Context, app port.GitHubOAuthApp) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.githubApp = app
	return nil
}

func (s *Store) DeleteGitHubOAuthApp(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.githubApp = port.GitHubOAuthApp{}
	return nil
}

func (s *Store) Settings(context.Context) (port.SiteSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return port.SiteSettings{BrandName: s.brandName, RegistrationOpen: !s.registrationClosed}, nil
}

func (s *Store) SetRegistrationOpen(_ context.Context, open bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registrationClosed = !open
	return nil
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

type storedPlaylist struct {
	id, owner, name  string
	favorites        bool
	items            []port.PlaylistItem
	created, updated time.Time
	shareID          string
}

func (p *storedPlaylist) cover() *port.Cover {
	for i := len(p.items) - 1; i >= 0; i-- {
		if m := p.items[i].Meta; m.CoverURL != "" || m.CoverKey != "" {
			return &port.Cover{URL: m.CoverURL, Key: m.CoverKey}
		}
	}
	return nil
}

// 调用方持有 s.mu。
func (s *Store) playlist(ownerID, id string) *storedPlaylist {
	for _, p := range s.playlists {
		if p.id == id && p.owner == ownerID {
			return p
		}
	}
	return nil
}

func (s *Store) MemberPlaylists(_ context.Context, ownerID string) ([]port.PlaylistRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var lists []port.PlaylistRecord
	for _, p := range s.playlists {
		if p.owner != ownerID {
			continue
		}
		r := port.PlaylistRecord{
			ID:         p.id,
			Name:       p.name,
			Favorites:  p.favorites,
			TrackCount: len(p.items),
			CreatedAt:  p.created,
			UpdatedAt:  p.updated,
			Cover:      p.cover(),
			ShareID:    p.shareID,
		}
		if p.favorites {
			lists = append([]port.PlaylistRecord{r}, lists...)
		} else {
			lists = append(lists, r)
		}
	}
	return lists, nil
}

func (s *Store) CreatePlaylist(_ context.Context, ownerID, name string, limit int) (port.PlaylistRecord, error) {
	if err := checkText(name); err != nil {
		return port.PlaylistRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.newPlaylist(ownerID, name, limit)
	if err != nil {
		return port.PlaylistRecord{}, err
	}
	return port.PlaylistRecord{ID: p.id, Name: name, CreatedAt: p.created, UpdatedAt: p.updated}, nil
}

func (s *Store) ImportPlaylist(_ context.Context, ownerID, name string, items []port.PlaylistItem, limit int) (port.PlaylistRecord, error) {
	if err := checkText(name); err != nil {
		return port.PlaylistRecord{}, err
	}
	seen := make(map[track.Track]bool, len(items))
	for _, item := range items {
		m := item.Meta
		if err := checkText(item.Track.ID, m.Title, m.Artist, m.Album, m.CoverURL, string(m.CoverKey), m.PageURL); err != nil {
			return port.PlaylistRecord{}, err
		}
		if seen[item.Track] {
			return port.PlaylistRecord{}, fmt.Errorf("歌单里已经有 %s/%s：违反主键约束", item.Track.Source, item.Track.ID)
		}
		seen[item.Track] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.newPlaylist(ownerID, name, limit)
	if err != nil {
		return port.PlaylistRecord{}, err
	}
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		item.Meta.Duration = item.Meta.Duration.Truncate(time.Millisecond)
		p.items = append(p.items, item)
	}
	return port.PlaylistRecord{ID: p.id, Name: name, TrackCount: len(p.items), CreatedAt: p.created, UpdatedAt: p.updated, Cover: p.cover()}, nil
}

// 调用方持有 s.mu。
func (s *Store) newPlaylist(ownerID, name string, limit int) (*storedPlaylist, error) {
	n := 0
	for _, p := range s.playlists {
		if p.owner == ownerID {
			n++
		}
	}
	if n >= limit {
		return nil, port.ErrPlaylistLimit
	}
	now := time.Now()
	p := &storedPlaylist{id: s.newID(), owner: ownerID, name: name, created: now, updated: now}
	s.playlists = append(s.playlists, p)
	return p, nil
}

func (s *Store) RenamePlaylist(_ context.Context, ownerID, id, name string) error {
	if err := checkText(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.playlist(ownerID, id)
	switch {
	case p == nil:
		return port.ErrPlaylistNotFound
	case p.favorites:
		return port.ErrFavoritesFixed
	}
	p.name = name
	p.updated = time.Now()
	return nil
}

func (s *Store) DeletePlaylist(_ context.Context, ownerID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.playlist(ownerID, id)
	switch {
	case p == nil:
		return port.ErrPlaylistNotFound
	case p.favorites:
		return port.ErrFavoritesFixed
	}
	s.playlists = slices.DeleteFunc(s.playlists, func(other *storedPlaylist) bool { return other == p })
	return nil
}

func (s *Store) PlaylistItems(_ context.Context, ownerID, id string) ([]port.PlaylistItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.playlist(ownerID, id)
	if p == nil {
		return nil, port.ErrPlaylistNotFound
	}
	return p.display(), nil
}

// 新加入的在前。
func (p *storedPlaylist) display() []port.PlaylistItem {
	items := make([]port.PlaylistItem, 0, len(p.items))
	for i := len(p.items) - 1; i >= 0; i-- {
		items = append(items, p.items[i])
	}
	return items
}

func (s *Store) AddPlaylistItem(_ context.Context, ownerID, id string, item port.PlaylistItem, limit int) error {
	m := item.Meta
	if err := checkText(item.Track.ID, m.Title, m.Artist, m.Album, m.CoverURL, string(m.CoverKey), m.PageURL); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.playlist(ownerID, id)
	if p == nil {
		return port.ErrPlaylistNotFound
	}
	if slices.ContainsFunc(p.items, func(i port.PlaylistItem) bool { return i.Track == item.Track }) {
		return nil
	}
	if len(p.items) >= limit {
		return port.ErrPlaylistLimit
	}
	item.Meta.Duration = item.Meta.Duration.Truncate(time.Millisecond)
	p.items = append(p.items, item)
	p.updated = time.Now()
	return nil
}

func (s *Store) RemovePlaylistItems(_ context.Context, ownerID, id string, tracks []track.Track) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.playlist(ownerID, id)
	if p == nil {
		return port.ErrPlaylistNotFound
	}
	n := len(p.items)
	p.items = slices.DeleteFunc(p.items, func(i port.PlaylistItem) bool { return slices.Contains(tracks, i.Track) })
	if len(p.items) < n {
		p.updated = time.Now()
	}
	return nil
}

func (s *Store) TrackPlaylists(_ context.Context, ownerID string, t track.Track) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := []string{}
	for _, p := range s.playlists {
		if p.owner == ownerID && slices.ContainsFunc(p.items, func(i port.PlaylistItem) bool { return i.Track == t }) {
			ids = append(ids, p.id)
		}
	}
	return ids, nil
}

func (s *Store) CopyPlaylistItems(_ context.Context, ownerID, fromID, toID string, tracks []track.Track, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	from, to := s.playlist(ownerID, fromID), s.playlist(ownerID, toID)
	if from == nil || to == nil {
		return 0, port.ErrPlaylistNotFound
	}
	var fresh []port.PlaylistItem
	for _, t := range tracks {
		i := slices.IndexFunc(from.items, func(i port.PlaylistItem) bool { return i.Track == t })
		if i < 0 || slices.ContainsFunc(to.items, func(i port.PlaylistItem) bool { return i.Track == t }) ||
			slices.ContainsFunc(fresh, func(i port.PlaylistItem) bool { return i.Track == t }) {
			continue
		}
		fresh = append(fresh, from.items[i])
	}
	if len(fresh) == 0 {
		return 0, nil
	}
	if len(to.items)+len(fresh) > limit {
		return 0, port.ErrPlaylistLimit
	}
	slices.Reverse(fresh)
	to.items = append(to.items, fresh...)
	to.updated = time.Now()
	return len(fresh), nil
}

func (s *Store) MovePlaylistItems(_ context.Context, ownerID, id string, tracks []track.Track, before *track.Track) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.playlist(ownerID, id)
	if p == nil {
		return port.ErrPlaylistNotFound
	}
	order := make([]track.Track, len(p.items))
	for i, item := range p.items {
		order[len(order)-1-i] = item.Track
	}
	moved, err := playlist.Move(order, tracks, before)
	if err != nil || slices.Equal(moved, order) {
		return err
	}
	byTrack := make(map[track.Track]port.PlaylistItem, len(p.items))
	for _, item := range p.items {
		byTrack[item.Track] = item
	}
	for i, t := range moved {
		p.items[len(moved)-1-i] = byTrack[t]
	}
	p.updated = time.Now()
	return nil
}

func (s *Store) SetPlaylistShare(_ context.Context, ownerID, id, shareID string) error {
	if err := checkText(shareID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.playlist(ownerID, id)
	if p == nil {
		return port.ErrPlaylistNotFound
	}
	if other := s.shared(shareID); other != nil && other != p {
		return fmt.Errorf("分享 ID %s 已被其他歌单使用：违反唯一约束", shareID)
	}
	p.shareID = shareID
	return nil
}

// 调用方持有 s.mu。
func (s *Store) shared(shareID string) *storedPlaylist {
	for _, p := range s.playlists {
		if shareID != "" && p.shareID == shareID {
			return p
		}
	}
	return nil
}

func (s *Store) SharedPlaylist(_ context.Context, shareID string) (port.SharedPlaylist, error) {
	if err := checkText(shareID); err != nil {
		return port.SharedPlaylist{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.shared(shareID)
	if p == nil {
		return port.SharedPlaylist{}, port.ErrShareNotFound
	}
	owner := s.member(p.owner).member
	return port.SharedPlaylist{Name: p.name, Favorites: p.favorites, OwnerName: owner.Name, OwnerAvatarHash: owner.AvatarHash, Items: p.display()}, nil
}

func (s *Store) CopySharedPlaylist(_ context.Context, ownerID, shareID, name string, limit int) (port.PlaylistRecord, error) {
	if err := checkText(shareID, name); err != nil {
		return port.PlaylistRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.newPlaylist(ownerID, name, limit)
	if err != nil {
		return port.PlaylistRecord{}, err
	}
	from := s.shared(shareID)
	if from == nil {
		// 与 PostgreSQL 的事务一样先新建再复制，读不到分享时撤销新建的歌单。
		s.playlists = slices.DeleteFunc(s.playlists, func(other *storedPlaylist) bool { return other == p })
		return port.PlaylistRecord{}, port.ErrShareNotFound
	}
	p.items = slices.Clone(from.items)
	return port.PlaylistRecord{ID: p.id, Name: name, TrackCount: len(p.items), CreatedAt: p.created, UpdatedAt: p.updated, Cover: p.cover()}, nil
}
