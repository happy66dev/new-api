package constant

// UpstreamModelUnitsPerYuan 用户上游模型独立计费的金额单位：1 元 = 100000 单位（即 10^-5 元）喵。
// 历史沿革：早期按「分」（0.01 元）计费，5 位小数费率的小调用会被舍成 0 分；
// 升级后余额/可用/共享上限/费用结算/日志展示全链路都用该细粒度单位，支持 5 位小数精度喵。
const UpstreamModelUnitsPerYuan = 100000
