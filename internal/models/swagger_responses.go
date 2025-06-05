package models

// SwaggerSearchResultResponse 是一个专门为 Swagger 文档生成的辅助结构体。
// 它解决了 swag 工具无法正确解析泛型类型 response.APIResponse[models.SearchResult] 的问题。
// 在实际的 API 响应中，你的代码仍然使用泛型的 response.APIResponse[models.SearchResult]。
// 这个结构体的字段应该与你实际的 response.APIResponse 结构保持一致（除了 Data 字段的类型）。
type SwaggerSearchResultResponse struct {
	Code    int          `json:"code"`           // 业务自定义状态码，例如 0 代表成功，其他值代表特定错误。
	Message string       `json:"message"`        // 操作结果的文字描述，例如 "搜索成功" 或具体的错误信息。
	Data    SearchResult `json:"data,omitempty"` // 具体的搜索结果数据负载。使用 omitempty 可以在 Data 为空时不显示该字段。
}

// SwaggerErrorResponse 是一个专门为 Swagger 文档生成的辅助结构体，用于表示错误响应。
// 它解决了 swag 工具无法正确解析泛型类型 response.APIResponse[any] 或 response.APIResponse[nil] 的问题。
type SwaggerErrorResponse struct {
	Code    int         `json:"code"`           // 业务自定义错误码。
	Message string      `json:"message"`        // 错误的文字描述。
	Data    interface{} `json:"data,omitempty"` // 错误响应中 data 字段通常为 null 或不包含有效业务数据，这里使用 interface{}。
}

// SwaggerHealthCheckResponse 是一个专门为 Swagger 文档生成的辅助结构体，用于健康检查响应。
// 它解决了 swag 工具无法正确解析泛型类型 response.APIResponse[gin.H] 的问题。
type SwaggerHealthCheckResponse struct {
	Code    int                    `json:"code"`
	Message string                 `json:"message"`
	Data    map[string]interface{} `json:"data,omitempty"` // gin.H 本质上是 map[string]interface{}
}

// 注意：你需要确保上面这些 SwaggerXXXResponse 结构体的字段 (Code, Message, Data, Success, TraceID)
// 与你项目中 `github.com/Xushengqwer/gateway/pkg/response` 包实际生成的 JSON 字段名和结构一致。
// 如果不一致，你需要调整这里的字段名和 JSON 标签。

type SwaggerHotSearchTermsResponse struct {
	Code    int           `json:"code"`           // 业务自定义状态码，例如 0 代表成功，其他值代表特定错误。
	Message string        `json:"message"`        // 操作结果的文字描述，例如 "搜索成功" 或具体的错误信息。
	Data    HotSearchTerm `json:"data,omitempty"` // 告诉前端哪些词是热门的。
}
