package consts

type ContextKey string

const (
	ContextKeyAllowAllModel ContextKey = "allow_all_model"
	ContextKeyAllowModels   ContextKey = "allow_models"
	ContextKeyAuthKeyID     ContextKey = "auth_key_id"
)

const (
	ContextKeyGeminiStream ContextKey = "gemini_stream"
)
