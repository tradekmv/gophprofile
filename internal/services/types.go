package services

import (
	"github.com/tradekmv/gophprofile/internal/domain"
)

// UploadResult — возвращаемое значение AvatarService.Upload.
type UploadResult struct {
	Avatar  *domain.Avatar
	URL     string
	Message string
}
