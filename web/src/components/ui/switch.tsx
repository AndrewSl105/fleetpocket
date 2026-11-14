
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
        "peer group/switch win-field inline-flex shrink-0 items-center justify-center outline-none focus-visible:outline-1 focus-visible:outline-dotted focus-visible:outline-offset-2 disabled:cursor-not-allowed disabled:bg-background data-[size=default]:size-[16px] data-[size=sm]:size-[13px] [box-shadow:inset_0_0_0_1px_#1c5180] bg-[linear-gradient(135deg,#dcdcd7,#ffffff)] dark:bg-none",
        className
      )}
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className={cn(
          "pointer-events-none block text-[#21a121] data-[state=unchecked]:invisible group-data-[size=default]/switch:size-3 data-[state=checked]:visible group-data-[size=sm]/switch:size-2.5"
        )}
      >
        <CheckIcon className="size-full" strokeWidth={4} />
      </SwitchPrimitive.Thumb>
    </SwitchPrimitive.Root>
  )
}

export { Switch }
