package assets

import (
	"context"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect"
	assetsv1 "github.com/syss-io/executor/gen/proto/go/assets/v1"
	"github.com/syss-io/executor/gen/proto/go/assets/v1/assetsv1connect"
	domainv1 "github.com/syss-io/executor/gen/proto/go/domain/v1"
	"gorm.io/gorm"
)

type Service struct {
	DB *gorm.DB
}

var _ assetsv1connect.AssetsHandler = (*Service)(nil)

func (s *Service) CreateInstruction(ctx context.Context, req *connect.Request[assetsv1.CreateInstructionRequest]) (*connect.Response[assetsv1.CreateInstructionResponse], error) {
	slug := generateSlug(req.Msg.Name)

	instruction := &domainv1.InstructionORM{
		Slug:        slug,
		Body:        req.Msg.Content,
		Args:        req.Msg.Args,
		Description: req.Msg.Description,
		Version:     1,
		CreatedAt:   time.Now().Unix(),
		IsPublished: false,
	}

	if err := s.DB.Create(instruction).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&assetsv1.CreateInstructionResponse{
		Instruction: &domainv1.Instruction{
			Id:          instruction.Id,
			Slug:        instruction.Slug,
			Body:        instruction.Body,
			Args:        instruction.Args,
			Description: instruction.Description,
			Version:     instruction.Version,
			CreatedAt:   instruction.CreatedAt,
			IsPublished: instruction.IsPublished,
		},
	}), nil
}

func (s *Service) ListInstructions(ctx context.Context, req *connect.Request[assetsv1.ListInstructionsRequest]) (*connect.Response[assetsv1.ListInstructionsResponse], error) {
	var instructions []domainv1.InstructionORM
	if err := s.DB.Find(&instructions).Error; err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	result := make([]*domainv1.Instruction, len(instructions))
	for i, inst := range instructions {
		result[i] = &domainv1.Instruction{
			Id:          inst.Id,
			Slug:        inst.Slug,
			Body:        inst.Body,
			Args:        inst.Args,
			Description: inst.Description,
			Version:     inst.Version,
			CreatedAt:   inst.CreatedAt,
			IsPublished: inst.IsPublished,
		}
	}

	return connect.NewResponse(&assetsv1.ListInstructionsResponse{
		Instructions: result,
	}), nil
}

func generateSlug(name string) string {
	slug := strings.ToLower(name)
	slug = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	return slug
}
