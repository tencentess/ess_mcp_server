package custom

func init() {
	RegisterDescriptionSuffix("CreateFlowByFiles", "\n注意：调用此接口完成后合同即创建完成，无需再调用 StartFlow 接口。"+
		"调用此接口创建合同时，必须提供签署人的信息：个人签署方需要提供姓名和手机号，企业签署方需要提供企业名称、签署人姓名和手机号。")
}
