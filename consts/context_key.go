package consts

type ContextKey string

const (
	ContextKeyAllowAllModel ContextKey = "allow_all_model"
	ContextKeyAllowModels   ContextKey = "allow_models"
	ContextKeyAuthKeyID     ContextKey = "auth_key_id"
	ContextKeyAuthKeyIOLog  ContextKey = "auth_key_io_log"
)

const (
	ContextKeyGeminiStream ContextKey = "gemini_stream"
)
