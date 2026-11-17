
import * as React from "react"
import { cn } from "@/lib/utils"
import { CheckIcon } from "lucide-react"
import { Switch as SwitchPrimitive } from "radix-ui"

function Switch({
  className,
  size = "default",
  ...props
}: React.ComponentProps<typeof SwitchPrimitive.Root> & {
  size?: "sm" | "default"
}) {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      data-size={size}
      className={cn(
        "peer group/switch fm-field inline-flex shrink-0 items-center justify-center outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-signal disabled:cursor-not-allowed disabled:bg-background data-[size=default]:size-[20px] data-[size=sm]:size-[16px] data-[state=checked]:bg-ink",
        className
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className={cn(
          "pointer-events-none block text-paper data-[state=unchecked]:invisible group-data-[size=default]/switch:size-3 data-[state=checked]:visible group-data-[size=sm]/switch:size-2.5"
        )}
      >
        <CheckIcon className="size-full" strokeWidth={4} />
      </SwitchPrimitive.Thumb>
    </SwitchPrimitive.Root>
  )
}

export { Switch }
