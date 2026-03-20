package ai

import internal "github.com/dcosson/flex-agent-runtime/internal/ai"

type (
	Role                  = internal.Role
	Message               = internal.Message
	UserMessage           = internal.UserMessage
	AssistantMessage      = internal.AssistantMessage
	ToolResultMessage     = internal.ToolResultMessage
	ContentBlock          = internal.ContentBlock
	ContentType           = internal.ContentType
	TextContent           = internal.TextContent
	ThinkingContent       = internal.ThinkingContent
	ImageContent          = internal.ImageContent
	ToolCall              = internal.ToolCall
	Usage                 = internal.Usage
	UsageCost             = internal.UsageCost
	StopReason            = internal.StopReason
	Tool                  = internal.Tool
	Context               = internal.Context
	EventType             = internal.EventType
	AssistantMessageEvent = internal.AssistantMessageEvent
	EventStream           = internal.EventStream
	Model                 = internal.Model
	ModelCost             = internal.ModelCost
	ModelCompat           = internal.ModelCompat
	StreamOptions         = internal.StreamOptions
	SimpleStreamOptions   = internal.SimpleStreamOptions
	ThinkingLevel         = internal.ThinkingLevel
	ThinkingBudgets       = internal.ThinkingBudgets
	CacheRetention        = internal.CacheRetention
	ProviderError         = internal.ProviderError
	ProviderErrorCode     = internal.ProviderErrorCode
	ToolCallIDNormalizer  = internal.ToolCallIDNormalizer
	APIClient             = internal.APIClient
	EmbeddingAPIClient    = internal.EmbeddingAPIClient
	ProviderConfig        = internal.ProviderConfig
	ProviderEndpoint      = internal.ProviderEndpoint
	CustomProviderConfig  = internal.CustomProviderConfig
	CustomModelOpts       = internal.CustomModelOpts
)

const (
	RoleUser       = internal.RoleUser
	RoleAssistant  = internal.RoleAssistant
	RoleToolResult = internal.RoleToolResult

	ContentTypeText     = internal.ContentTypeText
	ContentTypeThinking = internal.ContentTypeThinking
	ContentTypeImage    = internal.ContentTypeImage
	ContentTypeToolCall = internal.ContentTypeToolCall

	StopReasonStop    = internal.StopReasonStop
	StopReasonLength  = internal.StopReasonLength
	StopReasonToolUse = internal.StopReasonToolUse
	StopReasonError   = internal.StopReasonError
	StopReasonAborted = internal.StopReasonAborted

	EventStart         = internal.EventStart
	EventTextStart     = internal.EventTextStart
	EventTextDelta     = internal.EventTextDelta
	EventTextEnd       = internal.EventTextEnd
	EventThinkingStart = internal.EventThinkingStart
	EventThinkingDelta = internal.EventThinkingDelta
	EventThinkingEnd   = internal.EventThinkingEnd
	EventToolCallStart = internal.EventToolCallStart
	EventToolCallDelta = internal.EventToolCallDelta
	EventToolCallEnd   = internal.EventToolCallEnd
	EventDone          = internal.EventDone
	EventError         = internal.EventError

	ThinkingMinimal = internal.ThinkingMinimal
	ThinkingLow     = internal.ThinkingLow
	ThinkingMedium  = internal.ThinkingMedium
	ThinkingHigh    = internal.ThinkingHigh
	ThinkingXHigh   = internal.ThinkingXHigh

	CacheRetentionNone  = internal.CacheRetentionNone
	CacheRetentionShort = internal.CacheRetentionShort
	CacheRetentionLong  = internal.CacheRetentionLong

	ErrContextOverflow = internal.ErrContextOverflow
	ErrRateLimit       = internal.ErrRateLimit
	ErrAuth            = internal.ErrAuth
	ErrServerError     = internal.ErrServerError
	ErrUnknown         = internal.ErrUnknown
)

var (
	NewEventStream               = internal.NewEventStream
	RegisterModel                = internal.RegisterModel
	GetModel                     = internal.GetModel
	GetModels                    = internal.GetModels
	GetModelProviders            = internal.GetModelProviders
	ClearModels                  = internal.ClearModels
	CalculateCost                = internal.CalculateCost
	ModelsEqual                  = internal.ModelsEqual
	SupportsXHigh                = internal.SupportsXHigh
	Stream                       = internal.Stream
	StreamSimple                 = internal.StreamSimple
	Complete                     = internal.Complete
	CompleteSimple               = internal.CompleteSimple
	BuildBaseOptions             = internal.BuildBaseOptions
	ClampReasoning               = internal.ClampReasoning
	AdjustMaxTokensForThinking   = internal.AdjustMaxTokensForThinking
	TransformMessages            = internal.TransformMessages
	IsContextOverflow            = internal.IsContextOverflow
	TimeToMillis                 = internal.TimeToMillis
	MillisToTime                 = internal.MillisToTime
	UnmarshalArguments           = internal.UnmarshalArguments
	UnmarshalArgumentsFromReader = internal.UnmarshalArgumentsFromReader
	RegisterAPIClient            = internal.RegisterAPIClient
	GetAPIClient                 = internal.GetAPIClient
	ClearAPIClients              = internal.ClearAPIClients
	RegisterEmbeddingAPIClient   = internal.RegisterEmbeddingAPIClient
	GetEmbeddingAPIClient        = internal.GetEmbeddingAPIClient
	ClearEmbeddingAPIClients     = internal.ClearEmbeddingAPIClients
	RegisterProviderConfig       = internal.RegisterProviderConfig
	GetProviderConfig            = internal.GetProviderConfig
	ListProviderConfigs          = internal.ListProviderConfigs
	UnregisterProviderConfig     = internal.UnregisterProviderConfig
	ClearProviderConfigs         = internal.ClearProviderConfigs
	ResolveEndpoint              = internal.ResolveEndpoint
	RegisterCustomProvider       = internal.RegisterCustomProvider
	RegisterCustomModel          = internal.RegisterCustomModel
)

const LargeArgumentThreshold = internal.LargeArgumentThreshold
