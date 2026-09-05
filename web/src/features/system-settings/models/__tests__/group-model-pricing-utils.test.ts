/* Copyright (C) 2023-2026 QuantumNous */
import { describe, expect, test } from 'vitest'

import {
  GROUP_BILLING_MODE_INHERIT,
  GROUP_BILLING_MODE_PER_CALL,
  GROUP_BILLING_MODE_PER_TOKEN,
  GROUP_BILLING_MODE_TIERED,
  type GroupPricingDraft,
  buildDraftFromOverride,
  draftToOverride,
  formatOverrideSummary,
  parseGroupBillingText,
  stringifyGroupPricing,
} from '../group-model-pricing-utils'

/** 造一个所有数值都留空（即全部继承全局）的草稿喵。 */
function emptyDraft(
  overrides: Partial<GroupPricingDraft> = {}
): GroupPricingDraft {
  return {
    modelName: 'deepseek-chat',
    billingMode: GROUP_BILLING_MODE_INHERIT,
    modelPrice: '',
    modelRatio: '',
    completionRatio: '',
    cacheRatio: '',
    createCacheRatio: '',
    imageRatio: '',
    audioRatio: '',
    audioCompletionRatio: '',
    billingExpr: '',
    ...overrides,
  }
}

describe('draftToOverride', () => {
  test('an all-empty draft writes an empty override', () => {
    const result = draftToOverride(emptyDraft())
    expect(result.ok).toBe(true)
    // 留空即继承全局：一个键都不能写，否则会把「继承」悄悄变成具体数值喵。
    if (result.ok) expect(result.override).toEqual({})
  })

  test('empty inputs are skipped while filled ones are written', () => {
    const result = draftToOverride(
      emptyDraft({ modelRatio: '0.27', completionRatio: '' })
    )
    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(result.override).toEqual({ model_ratio: 0.27 })
      // 补全倍率留空时这个键必须完全不存在，而不是写成 0 喵。
      expect('completion_ratio' in result.override).toBe(false)
    }
  })

  test('an explicit zero is preserved as a real free price', () => {
    const result = draftToOverride(emptyDraft({ cacheRatio: '0' }))
    expect(result.ok).toBe(true)
    if (result.ok) expect(result.override.cache_ratio).toBe(0)
  })

  test('per_token mode is written so it can force per-call models back to per-token', () => {
    const result = draftToOverride(
      emptyDraft({ billingMode: GROUP_BILLING_MODE_PER_TOKEN })
    )
    expect(result.ok).toBe(true)
    if (result.ok) expect(result.override.billing_mode).toBe('per_token')
  })

  test('inherit mode does not write a billing_mode key', () => {
    const result = draftToOverride(emptyDraft())
    expect(result.ok).toBe(true)
    if (result.ok) expect('billing_mode' in result.override).toBe(false)
  })

  test('per_call requires an explicit price', () => {
    // 喵~防御：前端看不到全局按次价，所以强制要求填单价，避免静默变成 0 元/次喵。
    const missing = draftToOverride(
      emptyDraft({ billingMode: GROUP_BILLING_MODE_PER_CALL })
    )
    expect(missing.ok).toBe(false)
    if (!missing.ok) {
      expect(missing.messageKey).toBe('Per-request price is required')
    }

    const filled = draftToOverride(
      emptyDraft({
        billingMode: GROUP_BILLING_MODE_PER_CALL,
        modelPrice: '0.02',
      })
    )
    expect(filled.ok).toBe(true)
    if (filled.ok) {
      expect(filled.override.billing_mode).toBe('per_call')
      expect(filled.override.model_price).toBe(0.02)
    }
  })

  test('rejects negative and non-numeric values', () => {
    // 喵~防御：负价格会算出负额度（等于给用户返钱），必须在写入前拦下来喵。
    const negative = draftToOverride(emptyDraft({ modelRatio: '-1' }))
    expect(negative.ok).toBe(false)
    if (!negative.ok) {
      expect(negative.messageKey).toBe('Pricing values cannot be negative')
    }

    for (const dirty of ['abc', 'NaN', 'Infinity', '1e999']) {
      const result = draftToOverride(emptyDraft({ modelRatio: dirty }))
      expect(result.ok, dirty).toBe(false)
      if (!result.ok) {
        expect(result.messageKey, dirty).toBe(
          'Pricing values must be finite numbers'
        )
      }
    }
  })

  test('tiered mode only needs a non-empty expression', () => {
    const missing = draftToOverride(
      emptyDraft({ billingMode: GROUP_BILLING_MODE_TIERED, billingExpr: '  ' })
    )
    expect(missing.ok).toBe(false)
    if (!missing.ok) {
      expect(missing.messageKey).toBe('Billing expression is required')
    }

    const filled = draftToOverride(
      emptyDraft({
        billingMode: GROUP_BILLING_MODE_TIERED,
        billingExpr: 'p * 0.27',
        // 阶梯模式下这些数值不该被写进定价覆盖，避免同一模型存在两套价格口径喵。
        modelRatio: '99',
        modelPrice: '99',
      })
    )
    expect(filled.ok).toBe(true)
    if (filled.ok) {
      expect(filled.override).toEqual({ billing_mode: 'tiered_expr' })
    }
  })
})

describe('buildDraftFromOverride', () => {
  test('unconfigured fields become empty strings, not zeros', () => {
    const draft = buildDraftFromOverride(
      'deepseek-chat',
      { model_ratio: 0.27 },
      undefined,
      undefined
    )
    expect(draft.modelRatio).toBe('0.27')
    // 未配置的字段必须显示成空，显示 0 会让用户误以为这个分组免费喵。
    expect(draft.completionRatio).toBe('')
    expect(draft.modelPrice).toBe('')
    expect(draft.billingMode).toBe(GROUP_BILLING_MODE_INHERIT)
  })

  test('an explicit zero round-trips as "0"', () => {
    const draft = buildDraftFromOverride(
      'deepseek-chat',
      { cache_ratio: 0 },
      undefined,
      undefined
    )
    expect(draft.cacheRatio).toBe('0')
  })

  test('a group-scoped tiered mode wins over pricing overrides', () => {
    const draft = buildDraftFromOverride(
      'deepseek-chat',
      { model_ratio: 0.27 },
      GROUP_BILLING_MODE_TIERED,
      'p * 2'
    )
    expect(draft.billingMode).toBe(GROUP_BILLING_MODE_TIERED)
    expect(draft.billingExpr).toBe('p * 2')
    // 阶梯计费下倍率对该分组已不生效，面板必须留空以免误导喵。
    expect(draft.modelRatio).toBe('')
  })

  test('handles a completely missing override', () => {
    // 喵~防御：三份配置都没这条记录时也要产出一份可编辑的空草稿喵。
    const draft = buildDraftFromOverride(
      'new-model',
      undefined,
      undefined,
      undefined
    )
    expect(draft.modelName).toBe('new-model')
    expect(draft.billingMode).toBe(GROUP_BILLING_MODE_INHERIT)
    expect(draft.modelRatio).toBe('')
  })

  test('drops non-finite numbers coming from a hand-edited JSON', () => {
    // 喵~防御：手改 JSON 塞进 NaN/Infinity 时按未配置处理，绝不带回配置里喵。
    const draft = buildDraftFromOverride(
      'deepseek-chat',
      { model_ratio: Number.NaN, completion_ratio: Number.POSITIVE_INFINITY },
      undefined,
      undefined
    )
    expect(draft.modelRatio).toBe('')
    expect(draft.completionRatio).toBe('')
  })
})

describe('formatOverrideSummary', () => {
  test('reports the billing mode that actually applies', () => {
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

  test('handles a missing override', () => {
    expect(formatOverrideSummary(undefined, undefined)).toBe('Inherit global')
  })
})

describe('parseGroupBillingText / stringifyGroupPricing', () => {
  test('parses a well-formed two-level map', () => {
    expect(parseGroupBillingText('{"vip":{"m":"tiered_expr"}}')).toEqual({
      vip: { m: 'tiered_expr' },
    })
  })

  test('falls back to an empty object for unusable input', () => {
    // 喵~防御：坏 JSON 绝不能让编辑器崩掉，一律回落空对象喵。
    for (const bad of ['', '   ', '{oops', undefined]) {
      expect(parseGroupBillingText(bad)).toEqual({})
    }
  })

  test('serializes an empty config as {} rather than an empty string', () => {
    // 后端校验只认 JSON 对象，空串会被判成非法配置喵。
    expect(stringifyGroupPricing({})).toBe('{}')
    expect(stringifyGroupPricing(null)).toBe('{}')
    expect(stringifyGroupPricing(undefined)).toBe('{}')
  })

  test('round-trips a config through stringify and parse', () => {
    const config = { vip: { 'deepseek-chat': 'tiered_expr' } }
    expect(parseGroupBillingText(stringifyGroupPricing(config))).toEqual(config)
  })
})
