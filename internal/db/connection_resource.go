package db

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"design-cloud-engine/internal/model"

	"design-cloud-engine/internal/contract"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrResourceNotFound   = errors.New("connection resource not found")
	ErrResourceSlugExists = errors.New("connection resource slug already exists")
)

type ConnectionResourceRepo struct {
	store *Store
}

func (r *ConnectionResourceRepo) List(ctx context.Context, limit, offset int, protocol, query string) ([]model.ConnectionResource, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	var list []model.ConnectionResource
	var total int64

	q := r.store.db.WithContext(ctx).Model(&model.ConnectionResource{})
	if protocol != "" {
		p, err := contract.ParseProtocol(protocol)
		if err != nil {
			return nil, 0, contract.NewInvalidParamError(err.Error())
		}
		q = q.Where("protocol = ?", p)
	}
	if query != "" {
		pattern := "%" + query + "%"
		q = q.Where("slug LIKE ? OR name LIKE ?", pattern, pattern)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("id DESC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *ConnectionResourceRepo) ListAccessibleForUser(ctx context.Context, username string) ([]model.ConnectionResource, error) {
	var list []model.ConnectionResource
	err := r.store.db.WithContext(ctx).
		Distinct("connection_resources.*").
		Model(&model.ConnectionResource{}).
		Joins("LEFT JOIN user_connection_resources ON user_connection_resources.resource_id = connection_resources.id").
		Where("connection_resources.public = true OR user_connection_resources.username = ?", username).
		Order("connection_resources.id ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (r *ConnectionResourceRepo) GetByID(ctx context.Context, id uint) (model.ConnectionResource, error) {
	var res model.ConnectionResource
	err := r.store.db.WithContext(ctx).First(&res, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.ConnectionResource{}, fmt.Errorf("%w: resource %d", contract.ErrNotFound, id)
		}
		return model.ConnectionResource{}, err
	}
	return res, nil
}

func (r *ConnectionResourceRepo) GetBySlug(ctx context.Context, slug string) (model.ConnectionResource, error) {
	var res model.ConnectionResource
	err := r.store.db.WithContext(ctx).Where("slug = ?", slug).Take(&res).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.ConnectionResource{}, fmt.Errorf("%w: resource slug %q", contract.ErrNotFound, slug)
		}
		return model.ConnectionResource{}, err
	}
	return res, nil
}

func (r *ConnectionResourceRepo) GetAccessibleByID(ctx context.Context, id uint, username string) (model.ConnectionResource, error) {
	res, err := r.GetByID(ctx, id)
	if err != nil {
		return model.ConnectionResource{}, err
	}
	if res.Public {
		return res, nil
	}

	var count int64
	err = r.store.db.WithContext(ctx).Model(&model.UserConnectionResource{}).
		Where("username = ? AND resource_id = ?", username, id).
		Count(&count).Error
	if err != nil {
		return model.ConnectionResource{}, err
	}
	if count == 0 {
		return model.ConnectionResource{}, fmt.Errorf("%w: access denied to resource %d", contract.ErrNotFound, id)
	}
	return res, nil
}

func (r *ConnectionResourceRepo) Create(ctx context.Context, res *model.ConnectionResource) error {
	if res.Nodes == nil {
		res.Nodes = []string{}
	}
	if res.NodeGroups == nil {
		res.NodeGroups = []string{}
	}
	if err := r.store.db.WithContext(ctx).Create(res).Error; err != nil {
		if IsUniqueConstraintError(err) {
			return ErrResourceSlugExists
		}
		return err
	}
	return nil
}

func (r *ConnectionResourceRepo) Update(ctx context.Context, res *model.ConnectionResource) error {
	if res.Nodes == nil {
		res.Nodes = []string{}
	}
	if res.NodeGroups == nil {
		res.NodeGroups = []string{}
	}
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if res.ID == 0 {
			return fmt.Errorf("%w: resource id is required", contract.ErrNotFound)
		}
		// 读出 CreatedAt：供响应回填；更新用 Select 字段列表，避免 Save 把零值写回。
		var existing model.ConnectionResource
		if err := tx.Select("created_at").First(&existing, res.ID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: resource %d", contract.ErrNotFound, res.ID)
			}
			return err
		}
		res.CreatedAt = existing.CreatedAt
		res.UpdatedAt = time.Now().UTC()
		if err := tx.Model(&model.ConnectionResource{}).Where("id = ?", res.ID).
			Select("Slug", "Name", "Description", "MaxTotal", "Protocol", "Nodes", "NodeGroups", "Display", "Port", "MaxPerUser", "VNCConfig", "Public", "UpdatedAt").
			Updates(res).Error; err != nil {
			if IsUniqueConstraintError(err) {
				return ErrResourceSlugExists
			}
			return err
		}
		if res.Public {
			// 改为公开时清理已有映射
			if err := tx.Where("resource_id = ?", res.ID).Delete(&model.UserConnectionResource{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *ConnectionResourceRepo) Delete(ctx context.Context, id uint) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Delete(&model.ConnectionResource{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("%w: resource %d", contract.ErrNotFound, id)
		}
		if err := tx.Where("resource_id = ? AND status = ?", id, model.ConnectionStopped).Delete(&model.Connection{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Connection{}).Where("resource_id = ?", id).Update("resource_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Where("resource_id = ?", id).Delete(&model.UserConnectionResource{}).Error; err != nil {
			return err
		}
		return nil
	})
}

// User mappings

// resourceAuthorizedUserSelect is enough for admin authorization UIs (no password/preference).
const resourceAuthorizedUserSelect = "users.id, users.username, users.uid, users.status, users.type, users.role, users.max_connections, users.created_at, users.updated_at"

func (r *ConnectionResourceRepo) ListUsers(ctx context.Context, resourceID uint) ([]model.User, error) {
	var users []model.User
	err := r.store.db.WithContext(ctx).
		Model(&model.User{}).
		Select(resourceAuthorizedUserSelect).
		Joins("INNER JOIN user_connection_resources ON user_connection_resources.username = users.username").
		Where("user_connection_resources.resource_id = ?", resourceID).
		Where("users.type = ?", model.UserTypeSystem).
		Order("users.username ASC").
		Find(&users).Error
	return users, err
}

func (r *ConnectionResourceRepo) ListUsersPage(ctx context.Context, resourceID uint, limit, offset int) ([]model.User, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	base := r.store.db.WithContext(ctx).
		Model(&model.User{}).
		Joins("INNER JOIN user_connection_resources ON user_connection_resources.username = users.username").
		Where("user_connection_resources.resource_id = ?", resourceID).
		Where("users.type = ?", model.UserTypeSystem)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var users []model.User
	if err := base.Select(resourceAuthorizedUserSelect).Order("users.username ASC").Limit(limit).Offset(offset).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func (r *ConnectionResourceRepo) ReplaceUsers(ctx context.Context, resourceID uint, usernames []string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("resource_id = ?", resourceID).Delete(&model.UserConnectionResource{}).Error; err != nil {
			return err
		}
		if len(usernames) == 0 {
			return nil
		}
		var mappings []model.UserConnectionResource
		seen := make(map[string]struct{}, len(usernames))
		for _, name := range usernames {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			mappings = append(mappings, model.UserConnectionResource{
				Username:   name,
				ResourceID: resourceID,
			})
		}
		if len(mappings) == 0 {
			return nil
		}
		return tx.Create(&mappings).Error
	})
}

func (r *ConnectionResourceRepo) PatchUsers(ctx context.Context, resourceID uint, addNames, removeNames []string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(removeNames) > 0 {
			if err := tx.Where("resource_id = ? AND username IN ?", resourceID, removeNames).Delete(&model.UserConnectionResource{}).Error; err != nil {
				return err
			}
		}
		if len(addNames) == 0 {
			return nil
		}
		seen := make(map[string]struct{}, len(addNames))
		mappings := make([]model.UserConnectionResource, 0, len(addNames))
		for _, name := range addNames {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			mappings = append(mappings, model.UserConnectionResource{
				Username:   name,
				ResourceID: resourceID,
			})
		}
		if len(mappings) == 0 {
			return nil
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&mappings).Error
	})
}

// ListAvoidDisplaysForLaunch returns display numbers on node reserved by other X-display resources (VNC/XRDP) with fixed display.
// Fixed-display resources are few; load rows with display set, then match nodeID against Nodes (JSON string array) in memory.
func (r *ConnectionResourceRepo) ListAvoidDisplaysForLaunch(ctx context.Context, nodeID string, excludeResourceID uint) ([]int, error) {
	var list []model.ConnectionResource
	err := r.store.db.WithContext(ctx).
		Select("display", "nodes").
		Where("protocol IN ? AND display IS NOT NULL AND id <> ?", []contract.Protocol{contract.VNCProtocol, contract.XRDPProtocol}, excludeResourceID).
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	seen := make(map[int]struct{})
	for _, res := range list {
		if !slices.Contains(res.Nodes, nodeID) || res.Display == nil {
			continue
		}
		seen[*res.Display] = struct{}{}
	}
	return slices.Sorted(maps.Keys(seen)), nil
}
