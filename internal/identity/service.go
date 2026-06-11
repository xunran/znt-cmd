package identity

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"znt/internal/contracts"
	"znt/pkg/idgen"
)

type Service interface {
	ResolveMember(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID, externalUserID string) (contracts.GroupMemberProfile, bool, error)
	ListGroupMembers(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID) ([]contracts.GroupMemberProfile, error)
	UpsertMember(ctx context.Context, profile contracts.GroupMemberProfile) (contracts.GroupMemberProfile, error)
}

type Store interface {
	SaveMember(ctx context.Context, profile contracts.GroupMemberProfile) error
	ResolveMember(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID, externalUserID string) (contracts.GroupMemberProfile, bool, error)
	ListGroupMembers(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID) ([]contracts.GroupMemberProfile, error)
}

type InMemoryService struct {
	mu         sync.RWMutex
	members    map[string]contracts.GroupMemberProfile
	byExternal map[string]string
	store      Store
	now        func() time.Time
}

func NewInMemoryService() *InMemoryService {
	return NewInMemoryServiceWithStore(nil)
}

func NewInMemoryServiceWithStore(store Store) *InMemoryService {
	return &InMemoryService{
		members:    map[string]contracts.GroupMemberProfile{},
		byExternal: map[string]string{},
		store:      store,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

func (s *InMemoryService) UpsertMember(ctx context.Context, profile contracts.GroupMemberProfile) (contracts.GroupMemberProfile, error) {
	if profile.TenantID == "" || profile.GroupID == "" {
		return contracts.GroupMemberProfile{}, fmt.Errorf("tenant_id and group_id are required")
	}
	if profile.MemberID == "" {
		profile.MemberID = contracts.GroupMemberID(idgen.New("member"))
	}
	if profile.MemberType == "" {
		profile.MemberType = contracts.MemberTypeHuman
	}
	if profile.Status == "" {
		profile.Status = contracts.MemberStatusActive
	}
	if profile.LastSeenAt.IsZero() {
		profile.LastSeenAt = s.now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	memberKey := key(profile.TenantID, profile.GroupID, profile.MemberID)
	s.members[memberKey] = cloneMember(profile)
	if profile.ExternalUserID != "" {
		s.byExternal[externalKey(profile.TenantID, profile.GroupID, profile.ExternalUserID)] = memberKey
	}
	if s.store != nil {
		if err := s.store.SaveMember(ctx, profile); err != nil {
			return contracts.GroupMemberProfile{}, err
		}
	}
	return cloneMember(profile), nil
}

func (s *InMemoryService) ResolveMember(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID, externalUserID string) (contracts.GroupMemberProfile, bool, error) {
	if tenantID == "" || groupID == "" || externalUserID == "" {
		return contracts.GroupMemberProfile{}, false, nil
	}
	if s.store != nil {
		member, ok, err := s.store.ResolveMember(ctx, tenantID, groupID, externalUserID)
		if err != nil || ok {
			return member, ok, err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	memberKey, ok := s.byExternal[externalKey(tenantID, groupID, externalUserID)]
	if !ok {
		return contracts.GroupMemberProfile{}, false, nil
	}
	member, ok := s.members[memberKey]
	return cloneMember(member), ok, nil
}

func (s *InMemoryService) ListGroupMembers(ctx context.Context, tenantID contracts.TenantID, groupID contracts.GroupID) ([]contracts.GroupMemberProfile, error) {
	if s.store != nil {
		members, err := s.store.ListGroupMembers(ctx, tenantID, groupID)
		if err != nil || len(members) > 0 {
			return members, err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]contracts.GroupMemberProfile, 0)
	for _, member := range s.members {
		if member.TenantID == tenantID && member.GroupID == groupID {
			out = append(out, cloneMember(member))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left := out[i].DisplayName
		if left == "" {
			left = string(out[i].MemberID)
		}
		right := out[j].DisplayName
		if right == "" {
			right = string(out[j].MemberID)
		}
		return left < right
	})
	return out, nil
}

func key(tenantID contracts.TenantID, groupID contracts.GroupID, memberID contracts.GroupMemberID) string {
	return string(tenantID) + "\x00" + string(groupID) + "\x00" + string(memberID)
}

func externalKey(tenantID contracts.TenantID, groupID contracts.GroupID, externalUserID string) string {
	return string(tenantID) + "\x00" + string(groupID) + "\x00" + externalUserID
}

func cloneMember(in contracts.GroupMemberProfile) contracts.GroupMemberProfile {
	in.Aliases = append([]string(nil), in.Aliases...)
	in.Roles = append([]string(nil), in.Roles...)
	in.PermissionRefs = append([]string(nil), in.PermissionRefs...)
	if in.Metadata != nil {
		out := map[string]any{}
		for k, v := range in.Metadata {
			out[k] = v
		}
		in.Metadata = out
	}
	return in
}
