import { createFileRoute } from '@tanstack/react-router'

import { UpstreamModels } from '@/features/upstream-models'

export const Route = createFileRoute('/_authenticated/upstream-models/')({
  component: UpstreamModels,
})
