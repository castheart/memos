package v1

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/usememos/memos/internal/ai"
	"github.com/usememos/memos/internal/ai/audiollm"
	audiollmgemini "github.com/usememos/memos/internal/ai/audiollm/gemini"
	anyhostimagegen "github.com/usememos/memos/internal/ai/imagegen/anyhost"
	"github.com/usememos/memos/internal/ai/stt"
	sttopenai "github.com/usememos/memos/internal/ai/stt/openai"
	v1pb "github.com/usememos/memos/proto/gen/api/v1"
	storepb "github.com/usememos/memos/proto/gen/store"
)

const (
	maxTranscriptionAudioSizeBytes = 25 * MebiByte
	maxTranscriptionFilenameLength = 255
	maxImageGenerationPromptLength = 8000
)

var supportedTranscriptionContentTypes = map[string]bool{
	"audio/aac":    true,
	"audio/aiff":   true,
	"audio/flac":   true,
	"audio/mpeg":   true,
	"audio/mp3":    true,
	"audio/mp4":    true,
	"audio/mpga":   true,
	"audio/ogg":    true,
	"audio/wav":    true,
	"audio/x-wav":  true,
	"audio/x-flac": true,
	"audio/x-m4a":  true,
	"audio/webm":   true,
	"video/mp4":    true,
	"video/mpeg":   true,
	"video/webm":   true,
}

var supportedImageGenerationAspectRatios = map[string]bool{
	"1:1":  true,
	"4:3":  true,
	"3:4":  true,
	"16:9": true,
	"9:16": true,
}

// Transcribe transcribes an audio file using an instance AI provider.
func (s *APIV1Service) Transcribe(ctx context.Context, request *v1pb.TranscribeRequest) (*v1pb.TranscribeResponse, error) {
	user, err := s.fetchCurrentUser(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get current user: %v", err)
	}
	if user == nil {
		return nil, status.Errorf(codes.Unauthenticated, "user not authenticated")
	}

	if request.Audio == nil {
		return nil, status.Errorf(codes.InvalidArgument, "audio is required")
	}
	if request.Audio.GetUri() != "" {
		return nil, status.Errorf(codes.InvalidArgument, "audio uri is not supported")
	}
	content := request.Audio.GetContent()
	if len(content) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "audio content is required")
	}
	if len(content) > maxTranscriptionAudioSizeBytes {
		return nil, status.Errorf(codes.InvalidArgument, "audio file is too large; maximum size is 25 MiB")
	}
	filename := strings.TrimSpace(request.Audio.GetFilename())
	if len(filename) > maxTranscriptionFilenameLength {
		return nil, status.Errorf(codes.InvalidArgument, "filename is too long; maximum length is %d characters", maxTranscriptionFilenameLength)
	}
	contentType := strings.TrimSpace(request.Audio.GetContentType())
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	if !isSupportedTranscriptionContentType(contentType) {
		return nil, status.Errorf(codes.InvalidArgument, "audio content type %q is not supported", contentType)
	}

	aiSetting, err := s.Store.GetInstanceAISetting(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get AI setting: %v", err)
	}
	persisted := aiSetting.GetTranscription()

	providerID := persisted.GetProviderId()
	if providerID == "" {
		return nil, status.Errorf(codes.FailedPrecondition, "transcription is not configured")
	}

	provider, err := s.resolveAIProvider(aiSetting, providerID)
	if err != nil {
		return nil, err
	}

	model := persisted.GetModel()
	if model == "" {
		defaultModel, err := ai.DefaultTranscriptionModel(provider.Type)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		model = defaultModel
	}

	var text string
	switch provider.Type {
	case ai.ProviderOpenAI:
		text, err = s.transcribeViaSTT(ctx, provider, persisted, model, content, filename, contentType)
	case ai.ProviderGemini:
		text, err = s.transcribeViaAudioLLM(ctx, provider, persisted, model, content, contentType)
	default:
		return nil, status.Errorf(codes.FailedPrecondition,
			"provider type %q is not supported for transcription", provider.Type)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to transcribe audio: %v", err)
	}
	return &v1pb.TranscribeResponse{Text: text}, nil
}

// GenerateImage generates an image with Google Nano Banana Pro through the Anyhost Model API.
func (s *APIV1Service) GenerateImage(ctx context.Context, request *v1pb.GenerateImageRequest) (*v1pb.GenerateImageResponse, error) {
	user, err := s.fetchCurrentUser(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get current user: %v", err)
	}
	if user == nil {
		return nil, status.Errorf(codes.Unauthenticated, "user not authenticated")
	}

	prompt := strings.TrimSpace(request.GetPrompt())
	if prompt == "" {
		return nil, status.Errorf(codes.InvalidArgument, "prompt is required")
	}
	if len([]rune(prompt)) > maxImageGenerationPromptLength {
		return nil, status.Errorf(codes.InvalidArgument, "prompt is too long; maximum length is %d characters", maxImageGenerationPromptLength)
	}
	aspectRatio := strings.TrimSpace(request.GetAspectRatio())
	if aspectRatio == "" {
		aspectRatio = "1:1"
	}
	if !supportedImageGenerationAspectRatios[aspectRatio] {
		return nil, status.Errorf(codes.InvalidArgument, "aspect ratio %q is not supported", aspectRatio)
	}

	config, err := anyhostimagegen.LoadConfig()
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "image generation is not configured: %v", err)
	}
	client, err := anyhostimagegen.NewClient(config, anyhostimagegen.Options{})
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "image generation is not configured: %v", err)
	}

	response, err := client.Generate(ctx, anyhostimagegen.Request{
		Prompt:      prompt,
		AspectRatio: aspectRatio,
		UserID:      fmt.Sprintf("memos-user-%d", user.ID),
	})
	if err != nil {
		return nil, mapAnyhostImageGenerationError(err)
	}

	return &v1pb.GenerateImageResponse{
		Content:          response.Content,
		ContentType:      response.ContentType,
		Filename:         response.Filename,
		Model:            response.Model,
		GenerationId:     response.GenerationID,
		SettlementStatus: response.Usage.SettlementStatus,
		Cost:             response.Usage.Cost,
		Credits:          response.Usage.Credits,
	}, nil
}

func mapAnyhostImageGenerationError(err error) error {
	switch {
	case stderrors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "image generation was canceled")
	case stderrors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "image generation timed out")
	}

	var apiErr *anyhostimagegen.APIError
	if !stderrors.As(err, &apiErr) {
		return status.Errorf(codes.Internal, "failed to generate image: %v", err)
	}

	switch apiErr.Code {
	case "invalid_api_key":
		return status.Error(codes.Unauthenticated, "image generation credentials are invalid")
	case "insufficient_credits", "resource_budget_exceeded":
		return status.Errorf(codes.ResourceExhausted, "image generation is unavailable: %s", apiErr.Code)
	case "resource_disabled", "model_not_found", "model_retired", "model_unavailable", "model_capability_unsupported":
		return status.Errorf(codes.FailedPrecondition, "image generation is unavailable: %s", apiErr.Code)
	case "model_upstream_unavailable":
		return status.Error(codes.Unavailable, "image generation provider is temporarily unavailable")
	default:
		switch apiErr.StatusCode {
		case http.StatusUnauthorized:
			return status.Error(codes.Unauthenticated, "image generation credentials are invalid")
		case http.StatusPaymentRequired, http.StatusTooManyRequests:
			return status.Error(codes.ResourceExhausted, "image generation quota is unavailable")
		case http.StatusBadGateway, http.StatusServiceUnavailable:
			return status.Error(codes.Unavailable, "image generation provider is temporarily unavailable")
		default:
			return status.Error(codes.Internal, "image generation request failed")
		}
	}
}

func (*APIV1Service) transcribeViaSTT(
	ctx context.Context,
	provider ai.ProviderConfig,
	persisted *storepb.TranscriptionConfig,
	model string,
	content []byte,
	filename string,
	contentType string,
) (string, error) {
	transcriber, err := sttopenai.New(provider, stt.ApplyOptions(nil))
	if err != nil {
		return "", errors.Wrap(err, "failed to create STT transcriber")
	}
	resp, err := transcriber.Transcribe(ctx, stt.Request{
		Audio:       bytes.NewReader(content),
		Size:        int64(len(content)),
		Filename:    filename,
		ContentType: contentType,
		Model:       model,
		Prompt:      persisted.GetPrompt(),
		Language:    persisted.GetLanguage(),
	})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

func (*APIV1Service) transcribeViaAudioLLM(
	ctx context.Context,
	provider ai.ProviderConfig,
	persisted *storepb.TranscriptionConfig,
	model string,
	content []byte,
	contentType string,
) (string, error) {
	m, err := audiollmgemini.New(provider, audiollm.ApplyOptions(nil))
	if err != nil {
		return "", errors.Wrap(err, "failed to create audio LLM")
	}
	resp, err := m.GenerateFromAudio(ctx, audiollm.Request{
		Audio:        bytes.NewReader(content),
		Size:         int64(len(content)),
		ContentType:  contentType,
		Model:        model,
		Instructions: buildTranscriptionInstructions(persisted.GetPrompt(), persisted.GetLanguage()),
	})
	if err != nil {
		return "", err
	}
	if resp.FinishReason != audiollm.FinishStop {
		return "", errors.Errorf("transcription incomplete (finish reason: %s)", resp.FinishReason)
	}
	if strings.TrimSpace(resp.Text) == "" {
		return "", errors.New("transcription response did not include text")
	}
	return resp.Text, nil
}

func buildTranscriptionInstructions(prompt, language string) string {
	parts := []string{
		"Transcribe the audio accurately. Return only the transcript text. " +
			"Do not summarize, explain, or add content that is not spoken.",
	}
	if language = strings.TrimSpace(language); language != "" {
		parts = append(parts, "The input language is "+language+".")
	}
	if prompt = strings.TrimSpace(prompt); prompt != "" {
		parts = append(parts, "Context and spelling hints:\n"+prompt)
	}
	return strings.Join(parts, "\n\n")
}

func (*APIV1Service) resolveAIProvider(setting *storepb.InstanceAISetting, providerID string) (ai.ProviderConfig, error) {
	providers := make([]ai.ProviderConfig, 0, len(setting.GetProviders()))
	for _, provider := range setting.GetProviders() {
		if provider == nil {
			continue
		}
		providers = append(providers, convertAIProviderConfigFromStore(provider))
	}

	provider, err := ai.FindProvider(providers, providerID)
	if err != nil {
		return ai.ProviderConfig{}, status.Errorf(codes.FailedPrecondition, "transcription provider is not configured")
	}
	return *provider, nil
}

func convertAIProviderConfigFromStore(provider *storepb.AIProviderConfig) ai.ProviderConfig {
	return ai.ProviderConfig{
		ID:       provider.GetId(),
		Title:    provider.GetTitle(),
		Type:     convertAIProviderTypeFromStore(provider.GetType()),
		Endpoint: provider.GetEndpoint(),
		APIKey:   provider.GetApiKey(),
	}
}

func convertAIProviderTypeFromStore(providerType storepb.AIProviderType) ai.ProviderType {
	switch providerType {
	case storepb.AIProviderType_OPENAI:
		return ai.ProviderOpenAI
	case storepb.AIProviderType_GEMINI:
		return ai.ProviderGemini
	default:
		return ""
	}
}

func isSupportedTranscriptionContentType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(mediaType)
	return supportedTranscriptionContentTypes[mediaType]
}
