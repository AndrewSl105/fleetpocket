import * as React from "react"
import { cn } from "@/lib/utils"

function Textarea({ className, ...props }: React.ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "win-field flex field-sizing-content min-h-16 w-full rounded-none px-2 py-1.5 text-base text-foreground outline-none placeholder:text-muted-foreground focus-visible:outline-none disabled:cursor-not-allowed disabled:bg-background aria-invalid:text-destructive md:text-sm",
        className
      )}
      {...props}
    />
  )
}

export { Textarea }
