export function PageSpinner() {
  return (
    <div className="flex h-full items-center justify-center" role="status" aria-label="Loading">
      <div className="win-field h-5 w-40 overflow-hidden p-[3px]">
        <div className="fp-progress h-full" />
      </div>
    </div>
  )
}
