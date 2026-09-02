package service

import (
	"sync"
	"time"

	"topup-backend/internal/domain"
	"topup-backend/internal/repository"
)

type RBACService interface {
	CanAccess(role domain.Role, resource domain.PermissionResource) bool
	GetMyPermissions(role domain.Role) ([]domain.PermissionResource, error)
	GetAllRolePermissions() (map[domain.Role]map[domain.PermissionResource]bool, error)
	UpdateRolePermissions(permissions []domain.RolePermission) error
}

type rbacService struct {
	repo repository.RolePermissionRepository
	mu   sync.RWMutex
	// Cache permissions in memory for ultra-fast middleware checks
	cache map[domain.Role]map[domain.PermissionResource]bool
}

func NewRBACService(repo repository.RolePermissionRepository) RBACService {
	s := &rbacService{
		repo:  repo,
		cache: make(map[domain.Role]map[domain.PermissionResource]bool),
	}
	_ = s.refreshCache()
	return s
}

func (s *rbacService) refreshCache() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	perms, err := s.repo.GetAllPermissions()
	if err != nil {
		return err
	}

	newCache := make(map[domain.Role]map[domain.PermissionResource]bool)
	newCache[domain.RoleAdmin] = make(map[domain.PermissionResource]bool)
	newCache[domain.RoleOperator] = make(map[domain.PermissionResource]bool)

	for _, p := range perms {
		if _, exists := newCache[p.Role]; !exists {
			newCache[p.Role] = make(map[domain.PermissionResource]bool)
		}
		newCache[p.Role][p.Resource] = p.Allowed
	}

	s.cache = newCache
	return nil
}

func (s *rbacService) CanAccess(role domain.Role, resource domain.PermissionResource) bool {
	// SuperAdmin has absolute access to every resource
	if role == domain.RoleSuperAdmin {
		return true
	}

	// Roles management is strictly superadmin only
	if resource == domain.ResourceRoles {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if roleMap, exists := s.cache[role]; exists {
		if allowed, found := roleMap[resource]; found {
			return allowed
		}
	}

	return false
}

func (s *rbacService) GetMyPermissions(role domain.Role) ([]domain.PermissionResource, error) {
	// Superadmin gets all resources + roles
	if role == domain.RoleSuperAdmin {
		all := append([]domain.PermissionResource{}, domain.AllResources...)
		all = append(all, domain.ResourceRoles)
		return all, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var allowed []domain.PermissionResource
	if roleMap, exists := s.cache[role]; exists {
		for _, res := range domain.AllResources {
			if roleMap[res] {
				allowed = append(allowed, res)
			}
		}
	} else {
		// If cache is not ready, load from DB
		perms, err := s.repo.GetPermissionsByRole(role)
		if err != nil {
			return nil, err
		}
		for _, p := range perms {
			if p.Allowed {
				allowed = append(allowed, p.Resource)
			}
		}
	}

	return allowed, nil
}

func (s *rbacService) GetAllRolePermissions() (map[domain.Role]map[domain.PermissionResource]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Deep copy cache
	result := make(map[domain.Role]map[domain.PermissionResource]bool)
	for role, resMap := range s.cache {
		result[role] = make(map[domain.PermissionResource]bool)
		for k, v := range resMap {
			result[role][k] = v
		}
	}

	// Ensure all standard resources are present in the response
	roles := []domain.Role{domain.RoleAdmin, domain.RoleOperator}
	for _, r := range roles {
		if result[r] == nil {
			result[r] = make(map[domain.PermissionResource]bool)
		}
		for _, res := range domain.AllResources {
			if _, ok := result[r][res]; !ok {
				result[r][res] = false
			}
		}
	}

	return result, nil
}

func (s *rbacService) UpdateRolePermissions(permissions []domain.RolePermission) error {
	// Filter out any unauthorized roles
	var sanitized []domain.RolePermission
	now := time.Now()
	for _, p := range permissions {
		if p.Role == domain.RoleAdmin || p.Role == domain.RoleOperator {
			p.UpdatedAt = now
			sanitized = append(sanitized, p)
		}
	}

	if err := s.repo.BulkUpsertPermissions(sanitized); err != nil {
		return err
	}

	return s.refreshCache()
}
