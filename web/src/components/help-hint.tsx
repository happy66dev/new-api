/*
 Copyright (C) 2023-2026 QuantumNous

 This program is free software: you can redistribute it and/or modify
 it under the terms of the GNU Affero General Public License as published by
 the Free Software Foundation, either version 3 of the License, or
 (at your option) any later version.
*/
import { CircleQuestionMark } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { cn } from '@/lib/utils'

// HelpHintProps 定义字段帮助图标的输入：悬停短提示与点击展开的完整教程喵。
type HelpHintProps = {
  // hint 是鼠标悬停时展示的一句话提示，调用方需传入已翻译文案喵。
  hint: string
  // children 是点击图标后弹出的完整教程正文，调用方需传入已翻译内容喵。
  children: React.ReactNode
  // className 允许调用方微调图标颜色或尺寸，默认保持紧凑的图标按钮喵。
  className?: string
  // contentProps 允许调用方调整弹层方向与宽度，默认向下展开喵。
  contentProps?: React.ComponentProps<typeof PopoverContent>
}

// HelpHint 渲染统一的「⭕+?」帮助入口：悬停看短提示，点击看完整教程喵。
// 输入是已翻译的 hint 与 children；输出是一个无副作用的图标按钮喵。
// 边界条件：按钮可能位于 label 内，必须阻止默认行为，避免误聚焦或切换表单控件喵。
export function HelpHint(props: HelpHintProps) {
  // 读取当前语言的翻译函数，保证帮助按钮的无障碍标签随语言切换喵。
  const { t } = useTranslation()
  // trigger 同时作为 Popover 与 Tooltip 的触发器，让两种交互共用一个按钮喵。
  const trigger = (
    <PopoverTrigger
      aria-label={t('Show help')}
      onClick={(event) => {
        // 喵~防御：阻止 label 的默认激活，避免点击帮助图标时选中相邻输入框喵。
        event.preventDefault()
        // 喵~防御：阻止事件冒泡到外层 label，避免触发无关的点击处理喵。
        event.stopPropagation()
      }}
      render={
        <Button
          className={cn(
            'text-muted-foreground hover:text-foreground',
            props.className
          )}
          size='icon-xs'
          variant='ghost'
        />
      }
    >
      {/* 使用完整的圆形问号图标，形成「⭕+?」的视觉组合喵。 */}
      <CircleQuestionMark aria-hidden='true' className='size-3.5' />
    </PopoverTrigger>
  )
  return (
    <Popover>
      {/* 悬停提示延迟 300 毫秒，避免鼠标扫过多个字段时频繁弹出喵。 */}
      <Tooltip>
        {/* 复用同一个 trigger，使悬停与点击落在同一个可聚焦按钮上喵。 */}
        <TooltipTrigger delay={300} render={trigger} />
        <TooltipContent>
          {/* 悬停只展示一句话短提示，完整说明留给点击弹层喵。 */}
          <p>{props.hint}</p>
        </TooltipContent>
      </Tooltip>
      <PopoverContent
        align='start'
        side='bottom'
        sideOffset={6}
        {...props.contentProps}
        className={cn(
          'w-80 max-w-[calc(100vw-2rem)] text-sm',
          props.contentProps?.className
        )}
      >
        {/* 点击后展示完整教程正文，允许调用方传入段落或列表喵。 */}
        {props.children}
      </PopoverContent>
    </Popover>
  )
}
