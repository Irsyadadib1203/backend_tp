package repository

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"topup-backend/internal/domain"
)

type UserRepository interface {
	Create(user *domain.User) error
	FindByID(id uint) (*domain.User, error)
	FindByEmail(email string) (*domain.User, error)
	FindByAPIKey(key string) (*domain.User, *domain.APIKey, error)
	Update(user *domain.User) error
	UpdateBalance(userID uint, amount float64, mutationType domain.MutationType, refType, refID, desc string) error
	List(offset, limit int, role string) ([]domain.User, int64, error)
	Delete(id uint) error
	CreateAPIKey(apiKey *domain.APIKey) error
	GetAPIKeyByUserID(userID uint) (*domain.APIKey, error)
}

type userRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) Create(user *domain.User) error {
	return r.db.Create(user).Error
}

func (r *userRepository) FindByID(id uint) (*domain.User, error) {
	var user domain.User
	err := r.db.Preload("APIKey").Preload("IPWhitelists").First(&user, id).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *userRepository) FindByEmail(email string) (*domain.User, error) {
	var user domain.User
	err := r.db.Preload("APIKey").Where("email = ?", email).First(&user).Error
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *userRepository) FindByAPIKey(key string) (*domain.User, *domain.APIKey, error) {
	var apiKey domain.APIKey
	if err := r.db.Where("key = ? AND is_active = ?", key, true).First(&apiKey).Error; err != nil {
		return nil, nil, err
	}
	var user domain.User
	if err := r.db.Preload("IPWhitelists").First(&user, apiKey.UserID).Error; err != nil {
		return nil, nil, err
	}
	return &user, &apiKey, nil
}

func (r *userRepository) Update(user *domain.User) error {
	return r.db.Save(user).Error
}

func (r *userRepository) UpdateBalance(userID uint, amount float64, mutationType domain.MutationType, refType, refID, desc string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var user domain.User
		query := tx
		if tx.Dialector.Name() != "sqlite" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := query.First(&user, userID).Error; err != nil {
			return err
		}

		balanceBefore := user.Balance
		var balanceAfter float64

		if mutationType == domain.MutationDebit {
			if user.Balance < amount {
				return errors.New("insufficient balance")
			}
			balanceAfter = user.Balance - amount
		} else {
			balanceAfter = user.Balance + amount
		}

		if err := tx.Model(&domain.User{}).Where("id = ?", userID).Update("balance", balanceAfter).Error; err != nil {
			return err
		}

		mutation := domain.BalanceMutation{
			UserID:        userID,
			Type:          mutationType,
			Amount:        amount,
			BalanceBefore: balanceBefore,
			BalanceAfter:  balanceAfter,
			ReferenceType: refType,
			ReferenceID:   refID,
			Description:   desc,
		}
		return tx.Create(&mutation).Error
	})
}

func (r *userRepository) List(offset, limit int, role string) ([]domain.User, int64, error) {
	var users []domain.User
	var total int64

	query := r.db.Model(&domain.User{})
	if role != "" {
		query = query.Where("role = ?", role)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Preload("APIKey").Order("id DESC").Offset(offset).Limit(limit).Find(&users).Error
	return users, total, err
}

func (r *userRepository) Delete(id uint) error {
	return r.db.Delete(&domain.User{}, id).Error
}

func (r *userRepository) CreateAPIKey(apiKey *domain.APIKey) error {
	return r.db.Save(apiKey).Error
}

func (r *userRepository) GetAPIKeyByUserID(userID uint) (*domain.APIKey, error) {
	var apiKey domain.APIKey
	err := r.db.Where("user_id = ?", userID).First(&apiKey).Error
	if err != nil {
		return nil, err
	}
	return &apiKey, nil
}
