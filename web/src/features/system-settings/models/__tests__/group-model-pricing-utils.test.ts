/* Copyright (C) 2023-2026 QuantumNous */
import { describe, expect, test } from 'vitest'

import {
  GROUP_BILLING_MODE_INHERIT,
  GROUP_BILLING_MODE_PER_CALL,
  GROUP_BILLING_MODE_PER_TOKEN,
  GROUP_BILLING_MODE_TIERED,
  type GlobalPricingBases,
  type GroupPricingDraft,
  buildDraftFromOverride,
  draftToOverride,
  formatOverrideSummary,
  parseGroupBillingText,
  stringifyGroupPricing,
} from '../group-model-pricing-utils'

/** 造一个所有价格都留空（即全部继承全局）的草稿喵。 */
function emptyDraft(
  overrides: Partial<GroupPricingDraft> = {}
): GroupPricingDraft {
  return {
    modelName: 'deepseek-chat',
    billingMode: GROUP_BILLING_MODE_INHERIT,
    modelPrice: '',
    inputPrice: '',
    outputPrice: '',
    cachePrice: '',
    createCachePrice: '',
    imagePrice: '',
    audioPrice: '',
    audioOutputPrice: '',
    billingExpr: '',
    ...overrides,
  }
}

/** 全局基准：输入价 $0.27、音频输入价 $1.35（音频倍率 5）喵。 */
const GLOBAL_BASES: GlobalPricingBases = {
  inputPrice: 0.27,
  audioInputPrice: 1.35,
}

/** 断言换算后的倍率在浮点误差内等于期望值喵。 */
function expectClose(actual: number | undefined, expected: number): void {
  expect(actual).not.toBeUndefined()
  expect(Math.abs((actual as number) - expected)).toBeLessThan(1e-9)
}

describe('draftToOverride 价格转倍率', () => {
  test('全留空时写空覆盖项，一个键都不写', () => {
    const result = draftToOverride(emptyDraft(), GLOBAL_BASES)
    expect(result.ok).toBe(true)
    // 留空即继承全局：一个键都不能写，否则会把「继承」悄悄变成具体数值喵。
    if (result.ok) expect(result.override).toEqual({})
  })

  test('输入价 ÷ 2 换算成 model_ratio', () => {
    const result = draftToOverride(
      emptyDraft({ inputPrice: '0.27' }),
      GLOBAL_BASES
    )
    expect(result.ok).toBe(true)
    if (result.ok) {
      expectClose(result.override.model_ratio, 0.135)
    }
  })

  test('输出价 = 输入价 × completion_ratio', () => {
    const result = draftToOverride(
      emptyDraft({ inputPrice: '0.27', outputPrice: '1.1' }),
      GLOBAL_BASES
    )
    expect(result.ok).toBe(true)
    if (result.ok) {
      expectClose(result.override.completion_ratio, 1.1 / 0.27)
    }
  })

  test('缓存价、图片价等除以基准输入价', () => {
    const result = draftToOverride(
      emptyDraft({
        inputPrice: '0.27',
        cachePrice: '0.27',
        imagePrice: '1.35',
      }),
      GLOBAL_BASES
    )
    expect(result.ok).toBe(true)
    if (result.ok) {
      expectClose(result.override.cache_ratio, 1)
      expectClose(result.override.image_ratio, 5)
    }
  })

  test('没填输入价时回落到全局输入价作为换算基准', () => {
    // 只定制输出价、继承全局输入价 $0.27：completion_ratio = 1.1 / 0.27 喵。
    const result = draftToOverride(
      emptyDraft({ outputPrice: '1.1' }),
      GLOBAL_BASES
    )
    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(result.override.model_ratio).toBeUndefined()
      expectClose(result.override.completion_ratio, 1.1 / 0.27)
    }
  })

  test('音频输出价除以音频输入价得到 audio_completion_ratio', () => {
    const result = draftToOverride(
      emptyDraft({
        inputPrice: '0.27',
        audioPrice: '0.5',
        audioOutputPrice: '1.0',
      }),
      GLOBAL_BASES
    )
    expect(result.ok).toBe(true)
    if (result.ok) {
      expectClose(result.override.audio_ratio, 0.5 / 0.27)
      expectClose(result.override.audio_completion_ratio, 2)
    }
  })

  test('音频输出价没配音频输入价时回落到全局音频输入价', () => {
    const result = draftToOverride(
      emptyDraft({ inputPrice: '0.27', audioOutputPrice: '2.7' }),
      GLOBAL_BASES
    )
    expect(result.ok).toBe(true)
    if (result.ok) {
      // 全局音频输入价 1.35：audio_completion_ratio = 2.7 / 1.35 = 2 喵。
      expectClose(result.override.audio_completion_ratio, 2)
    }
  })

  test('继承模式也能单独改按次价', () => {
    // 全局是按次模型时，继承模式下面板会显示按次价框，这里要能写进 model_price 喵。
    const result = draftToOverride(
      emptyDraft({ modelPrice: '0.03' }),
      GLOBAL_BASES
    )
    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(result.override.model_price).toBe(0.03)
      expect(result.override.model_ratio).toBeUndefined()
    }
  })

  test('显式填 0 表示真免费，保留为 0', () => {
    const result = draftToOverride(
      emptyDraft({ inputPrice: '0' }),
      GLOBAL_BASES
    )
    expect(result.ok).toBe(true)
    if (result.ok) expect(result.override.model_ratio).toBe(0)
  })

  test('输入价为 0 却配了其它价格时无法换算，拦下并提示先填输入价', () => {
    const result = draftToOverride(
      emptyDraft({ inputPrice: '0', outputPrice: '1.1' }),
      GLOBAL_BASES
    )
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.messageKey).toBe(
        'Input price is required to convert per-token prices.'
      )
    }
  })

  test('负数与非数字一律拒绝', () => {
    // 喵~防御：负价格会算出负额度（等于给用户返钱），必须在写入前拦下来喵。
    const negative = draftToOverride(
      emptyDraft({ inputPrice: '-1' }),
      GLOBAL_BASES
    )
    expect(negative.ok).toBe(false)
    if (!negative.ok) {
      expect(negative.messageKey).toBe('Pricing values cannot be negative')
    }

    for (const dirty of ['abc', 'NaN', 'Infinity', '1e999']) {
      const result = draftToOverride(
        emptyDraft({ inputPrice: dirty }),
        GLOBAL_BASES
      )
      expect(result.ok, dirty).toBe(false)
      if (!result.ok) {
        expect(result.messageKey, dirty).toBe(
          'Pricing values must be finite numbers'
        )
      }
    }
  })

  test('per_token 模式会写 billing_mode 并换算倍率', () => {
    const result = draftToOverride(
      emptyDraft({
        billingMode: GROUP_BILLING_MODE_PER_TOKEN,
        inputPrice: '0.54',
      }),
      GLOBAL_BASES
    )
    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(result.override.billing_mode).toBe('per_token')
      expectClose(result.override.model_ratio, 0.27)
    }
  })

  test('按次模式必须有按次价，且只写按次价', () => {
    const missing = draftToOverride(
      emptyDraft({ billingMode: GROUP_BILLING_MODE_PER_CALL }),
      GLOBAL_BASES
    )
    expect(missing.ok).toBe(false)
    if (!missing.ok) {
      expect(missing.messageKey).toBe('Per-request price is required')
    }

    const filled = draftToOverride(
      emptyDraft({
        billingMode: GROUP_BILLING_MODE_PER_CALL,
        modelPrice: '0.02',
        // 按次模式下输入价不该被理会，倍率一律不写喵。
        inputPrice: '0.27',
      }),
      GLOBAL_BASES
    )
    expect(filled.ok).toBe(true)
    if (filled.ok) {
      expect(filled.override).toEqual({
        billing_mode: 'per_call',
        model_price: 0.02,
      })
    }
  })

  test('阶梯模式只需要表达式', () => {
    const missing = draftToOverride(
      emptyDraft({
        billingMode: GROUP_BILLING_MODE_TIERED,
        billingExpr: '  ',
      }),
      GLOBAL_BASES
    )
    expect(missing.ok).toBe(false)
    if (!missing.ok) {
      expect(missing.messageKey).toBe('Billing expression is required')
    }

    const filled = draftToOverride(
      emptyDraft({
        billingMode: GROUP_BILLING_MODE_TIERED,
        billingExpr: 'p * 0.27',
        // 阶梯模式下这些价格不该写进定价覆盖，避免同一模型存在两套价格口径喵。
        inputPrice: '99',
        outputPrice: '99',
      }),
      GLOBAL_BASES
    )
    expect(filled.ok).toBe(true)
    if (filled.ok) {
      expect(filled.override).toEqual({ billing_mode: 'tiered_expr' })
    }
  })
})

describe('buildDraftFromOverride 倍率转价格', () => {
  test('model_ratio × 2 还原输入价', () => {
    const draft = buildDraftFromOverride(
      'deepseek-chat',
      { model_ratio: 0.135 },
      undefined,
      undefined,
      GLOBAL_BASES
    )
    expect(draft.inputPrice).toBe('0.27')
    // 未配置的字段必须显示成空，显示 0 会让用户误以为这个分组免费喵。
    expect(draft.outputPrice).toBe('')
    expect(draft.modelPrice).toBe('')
    expect(draft.billingMode).toBe(GROUP_BILLING_MODE_INHERIT)
  })

  test('completion_ratio × 组内输入价还原输出价', () => {
    const draft = buildDraftFromOverride(
      'deepseek-chat',
      { model_ratio: 0.135, completion_ratio: 1.1 / 0.27 },
      undefined,
      undefined,
      GLOBAL_BASES
    )
    expect(draft.inputPrice).toBe('0.27')
    expect(draft.outputPrice).toBe('1.1')
  })

  test('没配组内输入价时按全局输入价换算输出价', () => {
    const draft = buildDraftFromOverride(
      'deepseek-chat',
      { completion_ratio: 1.1 / 0.27 },
      undefined,
      undefined,
      GLOBAL_BASES
    )
    // 输入价没配，必须显示空（继承全局），输出价按全局基准换算喵。
    expect(draft.inputPrice).toBe('')
    expect(draft.outputPrice).toBe('1.1')
  })

  test('音频输出价按音频输入价基准换算', () => {
    const draft = buildDraftFromOverride(
      'deepseek-chat',
      { audio_ratio: 0.5 / 0.27, audio_completion_ratio: 2 },
      undefined,
      undefined,
      GLOBAL_BASES
    )
    // 组内没配 model_ratio，音频输入价 = 全局输入价 0.27 × audio_ratio 喵。
    expect(draft.audioPrice).toBe('0.5')
    expect(draft.audioOutputPrice).toBe('1')
  })

  test('显式填 0 的倍率还原成 "0"', () => {
    const draft = buildDraftFromOverride(
      'deepseek-chat',
      { model_ratio: 0 },
      undefined,
      undefined,
      GLOBAL_BASES
    )
    expect(draft.inputPrice).toBe('0')
  })

  test('分组级阶梯模式优先，价格框留空', () => {
    const draft = buildDraftFromOverride(
      'deepseek-chat',
      { model_ratio: 0.135 },
      GROUP_BILLING_MODE_TIERED,
      'p * 2',
      GLOBAL_BASES
    )
    expect(draft.billingMode).toBe(GROUP_BILLING_MODE_TIERED)
    expect(draft.billingExpr).toBe('p * 2')
    // 阶梯计费下倍率对该分组已不生效，价格框必须留空以免误导喵。
    expect(draft.inputPrice).toBe('')
  })

  test('完全缺失的覆盖项产出空草稿', () => {
    // 喵~防御：三份配置都没这条记录时也要产出一份可编辑的空草稿喵。
    const draft = buildDraftFromOverride(
      'new-model',
      undefined,
      undefined,
      undefined,
      GLOBAL_BASES
    )
    expect(draft.modelName).toBe('new-model')
    expect(draft.billingMode).toBe(GROUP_BILLING_MODE_INHERIT)
    expect(draft.inputPrice).toBe('')
    expect(draft.outputPrice).toBe('')
  })

  test('全局基准缺失且组内也没倍率时全部留空', () => {
    // 全局都没给这个模型定价，任何倍率都换算不出价格，一律显示继承喵。
    const draft = buildDraftFromOverride(
      'unpriced-model',
      { completion_ratio: 2 },
      undefined,
      undefined,
      { inputPrice: 0, audioInputPrice: 0 }
    )
    expect(draft.inputPrice).toBe('')
    expect(draft.outputPrice).toBe('')
  })

  test('手改 JSON 塞进非有限数时按未配置处理', () => {
    // 喵~防御：手改 JSON 塞进 NaN/Infinity 时按未配置处理，绝不带回配置里喵。
    const draft = buildDraftFromOverride(
      'deepseek-chat',
      {
        model_ratio: Number.NaN,
        completion_ratio: Number.POSITIVE_INFINITY,
      },
      undefined,
      undefined,
      GLOBAL_BASES
    )
    expect(draft.inputPrice).toBe('')
    expect(draft.outputPrice).toBe('')
  })
})

describe('formatOverrideSummary', () => {
  test('报告实际生效的计费方式', () => {
    expect(formatOverrideSummary({ billing_mode: 'per_call' }, undefined)).toBe(
      'Per-request'
    )
    expect(
      formatOverrideSummary({ billing_mode: 'per_token' }, undefined)
    ).toBe('Per-token')
    expect(formatOverrideSummary({ model_ratio: 1 }, undefined)).toBe(
      'Inherit global'
    )
    // 分组级阶梯计费优先级最高，此时倍率对这个分组已经不生效了喵。
    expect(
      formatOverrideSummary({ billing_mode: 'per_call' }, 'tiered_expr')
    ).toBe('Tiered expression')
  })

  test('覆盖项缺失时兜底为继承全局', () => {
    expect(formatOverrideSummary(undefined, undefined)).toBe('Inherit global')
  })
})

describe('parseGroupBillingText / stringifyGroupPricing', () => {
  test('解析合法的两层映射', () => {
    expect(parseGroupBillingText('{"vip":{"m":"tiered_expr"}}')).toEqual({
      vip: { m: 'tiered_expr' },
    })
  })

  test('坏输入一律回落空对象', () => {
    // 喵~防御：坏 JSON 绝不能让编辑器崩掉，一律回落空对象喵。
    for (const bad of ['', '   ', '{oops', undefined]) {
      expect(parseGroupBillingText(bad)).toEqual({})
    }
  })

  test('空配置序列化成 {} 而不是空串', () => {
    // 后端校验只认 JSON 对象，空串会被判成非法配置喵。
    expect(stringifyGroupPricing({})).toBe('{}')
    expect(stringifyGroupPricing(null)).toBe('{}')
    expect(stringifyGroupPricing(undefined)).toBe('{}')
  })

  test('配置经过序列化与解析能原样往返', () => {
    const config = { vip: { 'deepseek-chat': 'tiered_expr' } }
    expect(parseGroupBillingText(stringifyGroupPricing(config))).toEqual(config)
  })
})
