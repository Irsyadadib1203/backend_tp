package service

import (
	"testing"
	"topup-backend/internal/domain"
)

type mockRolePermissionRepo struct {
	permissions []domain.RolePermission
}

func (m *mockRolePermissionRepo) GetPermissionsByRole(role domain.Role) ([]domain.RolePermission, error) {
	var list []domain.RolePermission
	for _, p := range m.permissions {
		if p.Role == role {
			list = append(list, p)
		}
	}
	return list, nil
}

func (m *mockRolePermissionRepo) GetAllPermissions() ([]domain.RolePermission, error) {
	return m.permissions, nil
}

func (m *mockRolePermissionRepo) UpsertPermission(role domain.Role, resource domain.PermissionResource, allowed bool) error {
	for i, p := range m.permissions {
		if p.Role == role && p.Resource == resource {
			m.permissions[i].Allowed = allowed
			return nil
		}
	}
	m.permissions = append(m.permissions, domain.RolePermission{
		Role:     role,
		Resource: resource,
		Allowed:  allowed,
	})
	return nil
}

func (m *mockRolePermissionRepo) BulkUpsertPermissions(permissions []domain.RolePermission) error {
	for _, np := range permissions {
		found := false
		for i, ep := range m.permissions {
			if ep.Role == np.Role && ep.Resource == np.Resource {
				m.permissions[i].Allowed = np.Allowed
				found = true
				break
			}
		}
		if !found {
			m.permissions = append(m.permissions, np)
		}
	}
	return nil
}

func (m *mockRolePermissionRepo) SeedDefaultPermissions() error {
	return nil
}

func TestRBACService_SuperAdminFullAccess(t *testing.T) {
	repo := &mockRolePermissionRepo{
		permissions: []domain.RolePermission{
			{Role: domain.RoleAdmin, Resource: domain.ResourceGames, Allowed: true},
			{Role: domain.RoleOperator, Resource: domain.ResourceTransactions, Allowed: true},
		},
	}

	svc := NewRBACService(repo)

	// Superadmin must have access to everything
	for _, res := range domain.AllResources {
		if !svc.CanAccess(domain.RoleSuperAdmin, res) {
			t.Errorf("expected superadmin to have access to %s", res)
		}
	}

	if !svc.CanAccess(domain.RoleSuperAdmin, domain.ResourceRoles) {
		t.Errorf("expected superadmin to have access to roles management")
	}
}

func TestRBACService_AdminAndOperatorPermissions(t *testing.T) {
	repo := &mockRolePermissionRepo{
		permissions: []domain.RolePermission{
			{Role: domain.RoleAdmin, Resource: domain.ResourceGames, Allowed: true},
			{Role: domain.RoleAdmin, Resource: domain.ResourceIPWhitelist, Allowed: false},
			{Role: domain.RoleOperator, Resource: domain.ResourceTransactions, Allowed: true},
			{Role: domain.RoleOperator, Resource: domain.ResourceGames, Allowed: false},
		},
	}

	svc := NewRBACService(repo)

	// Admin tests
	if !svc.CanAccess(domain.RoleAdmin, domain.ResourceGames) {
		t.Errorf("expected admin to have access to games")
	}
	if svc.CanAccess(domain.RoleAdmin, domain.ResourceIPWhitelist) {
		t.Errorf("expected admin NOT to have access to ip_whitelist")
	}
	if svc.CanAccess(domain.RoleAdmin, domain.ResourceRoles) {
		t.Errorf("expected admin NOT to have access to roles management")
	}

	// Operator tests
	if !svc.CanAccess(domain.RoleOperator, domain.ResourceTransactions) {
		t.Errorf("expected operator to have access to transactions")
	}
	if svc.CanAccess(domain.RoleOperator, domain.ResourceGames) {
		t.Errorf("expected operator NOT to have access to games")
	}
}

func TestRBACService_UpdatePermissionsDynamically(t *testing.T) {
	repo := &mockRolePermissionRepo{
		permissions: []domain.RolePermission{
			{Role: domain.RoleOperator, Resource: domain.ResourceKiosgamer, Allowed: false},
		},
	}

	svc := NewRBACService(repo)

	if svc.CanAccess(domain.RoleOperator, domain.ResourceKiosgamer) {
		t.Errorf("expected operator NOT to have access initially")
	}

	// Superadmin grants kiosgamer to operator
	err := svc.UpdateRolePermissions([]domain.RolePermission{
		{Role: domain.RoleOperator, Resource: domain.ResourceKiosgamer, Allowed: true},
	})
	if err != nil {
		t.Fatalf("failed to update permissions: %v", err)
	}

	// Now operator must have access
	if !svc.CanAccess(domain.RoleOperator, domain.ResourceKiosgamer) {
		t.Errorf("expected operator to have access after dynamic update")
	}
}
