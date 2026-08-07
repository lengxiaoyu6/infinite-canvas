package repository

import (
	"github.com/tigerowo/infinite-canvas/model"
)

// SaveStorageObject 保存存储对象记录。
func SaveStorageObject(object model.StorageObject) (model.StorageObject, error) {
	db, err := DB()
	if err != nil {
		return model.StorageObject{}, err
	}
	return object, db.Save(&object).Error
}

// UpdateStorageObjectPublicLink 更新存储对象公开地址。
func UpdateStorageObjectPublicLink(id string, publicURL string, directLinkID string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	return db.Model(&model.StorageObject{}).
		Where("id = ?", id).
		Updates(map[string]any{"public_url": publicURL, "direct_link_id": directLinkID}).Error
}

// GetStorageObject 根据 ID 获取存储对象。
func GetStorageObject(id string) (model.StorageObject, error) {
	db, err := DB()
	if err != nil {
		return model.StorageObject{}, err
	}
	var object model.StorageObject
	err = db.First(&object, "id = ?", id).Error
	return object, err
}

// DeleteStorageObjectRecord 删除存储对象记录（软删除）。
func DeleteStorageObjectRecord(id string) error {
	db, err := DB()
	if err != nil {
		return err
	}
	return db.Delete(&model.StorageObject{}, "id = ?", id).Error
}
