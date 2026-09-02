package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"topup-backend/internal/domain"
)

type RolePermissionRepository interface {
	GetPermissionsByRole(role domain.Role) ([]domain.RolePermission, error)
	GetAllPermissions() ([]domain.RolePermission, error)
	UpsertPermission(role domain.Role, resource domain.PermissionResource, allowed bool) error
	BulkUpsertPermissions(permissions []domain.RolePermission) error
	SeedDefaultPermissions() error
}

type rolePermissionRepository struct {
	db *gorm.DB
}

func NewRolePermissionRepository(db *gorm.DB) RolePermissionRepository {
	repo := &rolePermissionRepository{db: db}
	_ = repo.SeedDefaultPermissions()
	return repo
}

func (r *rolePermissionRepository) GetPermissionsByRole(role domain.Role) ([]domain.RolePermission, error) {
	var permissions []domain.RolePermission
	err := r.db.Where("role = ?", role).Find(&permissions).Error
	return permissions, err
}

func (r *rolePermissionRepository) GetAllPermissions() ([]domain.RolePermission, error) {
	var permissions []domain.RolePermission
	err := r.db.Order("role ASC, id ASC").Find(&permissions).Error
	return permissions, err
}

func (r *rolePermissionRepository) UpsertPermission(role domain.Role, resource domain.PermissionResource, allowed bool) error {
	var existing domain.RolePermission
	err := r.db.Where("role = ? AND resource = ?", role, resource).First(&existing).Error
	if err == nil {
		existing.Allowed = allowed
		return r.db.Save(&existing).Error
	}
	if err == gorm.ErrRecordNotFound {
		newPerm := domain.RolePermission{
			Role:     role,
			Resource: resource,
			Allowed:  allowed,
		}
		return r.db.Create(&newPerm).Error
	}
	return err
}

func (r *rolePermissionRepository) BulkUpsertPermissions(permissions []domain.RolePermission) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, p := range permissions {
			var existing domain.RolePermission
			err := tx.Where("role = ? AND resource = ?", p.Role, p.Resource).First(&existing).Error
			if err == nil {
				existing.Allowed = p.Allowed
				if err := tx.Save(&existing).Error; err != nil {
					return err
				}
			} else if err == gorm.ErrRecordNotFound {
				if err := tx.Create(&p).Error; err != nil {
					return err
				}
			} else {
				return err
			}
		}
		return nil
	})
}

// SeedDefaultPermissions initializes default access matrices for Admin and Operator if empty
func (r *rolePermissionRepository) SeedDefaultPermissions() error {
	// Operator default accessible modules
	operatorAllowed := map[domain.PermissionResource]bool{
		domain.ResourceDashboard:    true,
		domain.ResourceTransactions: true,
		domain.ResourceDeposits:     true,
		domain.ResourceDocs:         true,
	}

	// Admin default accessible modules (All except sensitive security / system logs by default)
	adminDisallowed := map[domain.PermissionResource]bool{
		domain.ResourceIPWhitelist: false,
		domain.ResourceSettings:    false,
	}

	var toInsert []domain.RolePermission

	// Seed for Admin
	for _, res := range domain.AllResources {
		var count int64
		r.db.Model(&domain.RolePermission{}).Where("role = ? AND resource = ?", domain.RoleAdmin, res).Count(&count)
		if count == 0 {
			allowed := true
			if isDisallowed, found := adminDisallowed[res]; found && !isDisallowed {
				allowed = false
			}
			toInsert = append(toInsert, domain.RolePermission{
				Role:     domain.RoleAdmin,
				Resource: res,
				Allowed:  allowed,
			})
		}
	}

	// Seed for Operator
	for _, res := range domain.AllResources {
		var count int64
		r.db.Model(&domain.RolePermission{}).Where("role = ? AND resource = ?", domain.RoleOperator, res).Count(&count)
		if count == 0 {
			allowed := false
			if isAllowed, found := operatorAllowed[res]; found && isAllowed {
				allowed = true
			}
			toInsert = append(toInsert, domain.RolePermission{
				Role:     domain.RoleOperator,
				Resource: res,
				Allowed:  allowed,
			})
		}
	}

	if len(toInsert) > 0 {
		return r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&toInsert).Error
	}
	return nil
}
