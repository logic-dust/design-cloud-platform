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
	ErrResourceInUse      = errors.New("connection resource in use")
	ErrResourceNameExists = errors.New("connection resource name already exists")
)

type ConnectionResourceRepo struct {
	store *Store
}

func (r *ConnectionResourceRepo) List(ctx context.Context, limit, offset int, protocol, nameLike string) ([]model.ConnectionResource, int64, error) {
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
	if nameLike != "" {
		q = q.Where("name LIKE ?", "%"+nameLike+"%")
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("id DESC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (r *ConnectionResourceRepo) ListAccessibleForUser(ctx context.Context, userID uint) ([]model.ConnectionResource, error) {
	var list []model.ConnectionResource
	err := r.store.db.WithContext(ctx).
		Distinct("connection_resources.*").
		Model(&model.ConnectionResource{}).
		Joins("LEFT JOIN user_connection_resources ON user_connection_resources.resource_id = connection_resources.id").
		Where("connection_resources.public = true OR user_connection_resources.user_id = ?", userID).
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

func (r *ConnectionResourceRepo) GetByName(ctx context.Context, name string) (model.ConnectionResource, error) {
	var res model.ConnectionResource
	err := r.store.db.WithContext(ctx).Where("name = ?", name).First(&res).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.ConnectionResource{}, fmt.Errorf("%w: resource name %q", contract.ErrNotFound, name)
		}
		return model.ConnectionResource{}, err
	}
	return res, nil
}

func (r *ConnectionResourceRepo) GetAccessibleByID(ctx context.Context, id uint, userID uint) (model.ConnectionResource, error) {
	res, err := r.GetByID(ctx, id)
	if err != nil {
		return model.ConnectionResource{}, err
	}
	if res.Public {
		return res, nil
	}

	var count int64
	err = r.store.db.WithContext(ctx).Model(&model.UserConnectionResource{}).
		Where("user_id = ? AND resource_id = ?", userID, id).
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
			return ErrResourceNameExists
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
			Select("Name", "Protocol", "Nodes", "NodeGroups", "Display", "Port", "MaxPerUser", "VNCConfig", "Public", "UpdatedAt").
			Updates(res).Error; err != nil {
			if IsUniqueConstraintError(err) {
				return ErrResourceNameExists
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
		var connCount int64
		if err := tx.Model(&model.Connection{}).Where("resource_id = ?", id).Count(&connCount).Error; err != nil {
			return err
		}
		if connCount > 0 {
			// 计数包含 stopped/unknown/history：删除规格会让这些实例失去 resource_id 归属。
			return fmt.Errorf("%w: resource %d has %d instances (including stopped and history)", ErrResourceInUse, id, connCount)
		}

		if err := tx.Where("resource_id = ?", id).Delete(&model.UserConnectionResource{}).Error; err != nil {
			return err
		}
		result := tx.Delete(&model.ConnectionResource{}, id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("%w: resource %d", contract.ErrNotFound, id)
		}
		return nil
	})
}

// User mappings
func (r *ConnectionResourceRepo) ListUsers(ctx context.Context, resourceID uint) ([]model.User, error) {
	var users []model.User
	err := r.store.db.WithContext(ctx).
		Model(&model.User{}).
		Joins("INNER JOIN user_connection_resources ON user_connection_resources.user_id = users.id").
		Where("user_connection_resources.resource_id = ?", resourceID).
		Order("users.id ASC").
		Find(&users).Error
	return users, err
}

func (r *ConnectionResourceRepo) ListUsersPage(ctx context.Context, resourceID uint, limit, offset int) ([]model.User, int64, error) {
	if err := validateLimit(limit); err != nil {
		return nil, 0, err
	}
	base := r.store.db.WithContext(ctx).
		Model(&model.User{}).
		Joins("INNER JOIN user_connection_resources ON user_connection_resources.user_id = users.id").
		Where("user_connection_resources.resource_id = ?", resourceID)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var users []model.User
	if err := base.Order("users.id ASC").Limit(limit).Offset(offset).Find(&users).Error; err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func (r *ConnectionResourceRepo) ReplaceUsers(ctx context.Context, resourceID uint, userIDs []uint) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("resource_id = ?", resourceID).Delete(&model.UserConnectionResource{}).Error; err != nil {
			return err
		}
		if len(userIDs) == 0 {
			return nil
		}
		var mappings []model.UserConnectionResource
		seen := make(map[uint]struct{}, len(userIDs))
		for _, uid := range userIDs {
			if _, ok := seen[uid]; ok {
				continue
			}
			seen[uid] = struct{}{}
			mappings = append(mappings, model.UserConnectionResource{
				UserID:     uid,
				ResourceID: resourceID,
			})
		}
		if len(mappings) == 0 {
			return nil
		}
		return tx.Create(&mappings).Error
	})
}

func (r *ConnectionResourceRepo) PatchUsers(ctx context.Context, resourceID uint, addIDs, removeIDs []uint) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(removeIDs) > 0 {
			if err := tx.Where("resource_id = ? AND user_id IN ?", resourceID, removeIDs).Delete(&model.UserConnectionResource{}).Error; err != nil {
				return err
			}
		}
		if len(addIDs) == 0 {
			return nil
		}
		seen := make(map[uint]struct{}, len(addIDs))
		mappings := make([]model.UserConnectionResource, 0, len(addIDs))
		for _, uid := range addIDs {
			if _, ok := seen[uid]; ok {
				continue
			}
			seen[uid] = struct{}{}
			mappings = append(mappings, model.UserConnectionResource{
				UserID:     uid,
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
