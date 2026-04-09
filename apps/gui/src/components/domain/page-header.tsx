interface PageHeaderProps {
  title: string
  children?: React.ReactNode
}

export function PageHeader({ title, children }: PageHeaderProps) {
  return (
    <div className="flex items-center justify-between border-b border-zinc-800/80 bg-zinc-950 px-4 py-2.5">
      <h1 className="text-[11px] font-semibold uppercase tracking-[.18em] text-zinc-400">
        {title}
      </h1>
      {children && <div className="flex items-center gap-2">{children}</div>}
    </div>
  )
}
