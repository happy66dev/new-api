package model

import "testing"

// TestNormalizeVirtualModelName 验证虚拟模型名称的规范化和非法输入防御喵。
func TestNormalizeVirtualModelName(t *testing.T) {
	// 定义正常输入、带前缀输入和非法输入，覆盖调用入口的主要边界喵。
	testCases := []struct {
		name        string
		input       string
		expected    string
		expectError bool
	}{
		{name: "plain name", input: "Research_Model-1", expected: "research_model-1"},
		{name: "virtual prefix", input: "virtual/Research_Model-1", expected: "research_model-1"},
		{name: "empty name", input: "", expectError: true},
		{name: "nested path", input: "team/model", expectError: true},
		{name: "unsupported unicode", input: "模型", expectError: true},
		{name: "space", input: "model name", expectError: true},
	}
	// 逐项执行名称校验并检查规范化结果，避免非法资源进入数据库喵。
	for _, testCase := range testCases {
		// 运行单个子测试，失败时明确指出输入边界喵。
		t.Run(testCase.name, func(t *testing.T) {
			// 调用被测名称规范化函数，获取结果和错误状态喵。
			normalizedName, err := NormalizeVirtualModelName(testCase.input)
			// 校验错误预期，避免错误输入被静默接受喵。
			if (err != nil) != testCase.expectError {
				t.Fatalf("NormalizeVirtualModelName(%q) error = %v, wantError = %v", testCase.input, err, testCase.expectError)
			}
			// 正常输入必须得到稳定的小写名称，便于 owner + name 唯一查询喵。
			if !testCase.expectError && normalizedName != testCase.expected {
				t.Fatalf("NormalizeVirtualModelName(%q) = %q, want %q", testCase.input, normalizedName, testCase.expected)
			}
		})
	}
}

// TestValidateVirtualModelConfiguration 验证模型所有权和安全参数边界喵。
func TestValidateVirtualModelConfiguration(t *testing.T) {
	// 构造一份合法模型作为基准，后续逐项验证防御条件喵。
	validModel := &VirtualModel{OwnerUserID: 7, NormalizedName: "private-model", DisplayName: "Private Model", TotalTimeoutSeconds: 120, MaxLoopRounds: 2}
	// 合法配置必须通过服务端校验，保证默认控制面可以保存喵。
	if err := ValidateVirtualModelConfiguration(validModel); err != nil {
		t.Fatalf("valid model rejected: %v", err)
	}
	// 空所有者必须被拒绝，防止客户端伪造或遗漏 owner 条件喵。
	invalidOwner := *validModel
	invalidOwner.OwnerUserID = 0
	if err := ValidateVirtualModelConfiguration(&invalidOwner); err == nil {
		t.Fatal("model without owner should be rejected")
	}
	// 超长循环超时必须被拒绝，防止请求占用资源无界增长喵。
	invalidTimeout := *validModel
	invalidTimeout.TotalTimeoutSeconds = 3601
	if err := ValidateVirtualModelConfiguration(&invalidTimeout); err == nil {
		t.Fatal("model with excessive timeout should be rejected")
	}
}
