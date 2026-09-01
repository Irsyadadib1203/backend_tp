package repository

import (
	"topup-backend/internal/domain"

	"gorm.io/gorm"
)

type ArticleRepository interface {
	List(publishedOnly bool, category string) ([]domain.Article, error)
	GetBySlug(slug string) (*domain.Article, error)
	GetByID(id uint) (*domain.Article, error)
	Create(a *domain.Article) error
	Update(a *domain.Article) error
	Delete(id uint) error
}

type articleRepository struct {
	db *gorm.DB
}

func NewArticleRepository(db *gorm.DB) ArticleRepository {
	return &articleRepository{db: db}
}

func (r *articleRepository) List(publishedOnly bool, category string) ([]domain.Article, error) {
	var articles []domain.Article
	q := r.db.Order("sort_order ASC, created_at DESC")
	if publishedOnly {
		q = q.Where("is_published = ?", true)
	}
	if category != "" && category != "Semua" {
		q = q.Where("category = ?", category)
	}
	err := q.Find(&articles).Error
	return articles, err
}

func (r *articleRepository) GetBySlug(slug string) (*domain.Article, error) {
	var a domain.Article
	if err := r.db.Where("slug = ?", slug).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *articleRepository) GetByID(id uint) (*domain.Article, error) {
	var a domain.Article
	if err := r.db.First(&a, id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *articleRepository) Create(a *domain.Article) error {
	return r.db.Create(a).Error
}

func (r *articleRepository) Update(a *domain.Article) error {
    updates := map[string]interface{}{
        "title":        a.Title,
        "slug":         a.Slug,
        "excerpt":      a.Excerpt,
        "content":      a.Content,
        "image_url":    a.ImageURL,
        "category":     a.Category,
		"read_time":    a.ReadTime,
        "is_published": a.IsPublished,
        "sort_order":   a.SortOrder,
    }

    return r.db.
        Model(&domain.Article{}).
        Where("id = ?", a.ID).
        Updates(updates).
        Error
}

func (r *articleRepository) Delete(id uint) error {
	return r.db.Delete(&domain.Article{}, id).Error
}
