import type { ReactNode } from "react";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { Button } from "./Button";

export type MenuItem = { label: string; title?: string; onSelect: () => void } | "separator";

// Menu is a Radix dropdown (keyboard navigation, Esc and outside click) with a Button trigger.
export function Menu({
  label,
  children,
  items,
  disabled = false,
  title,
  className = "",
  align = "start",
  side = "bottom",
}: {
  // Accessible name of the trigger; children are its visible content.
  label: string;
  children: ReactNode;
  items: MenuItem[];
  disabled?: boolean;
  title?: string;
  className?: string;
  align?: "start" | "end";
  side?: "top" | "bottom";
}) {
  return (
    <DropdownMenu.Root modal={false}>
      <DropdownMenu.Trigger asChild disabled={disabled}>
        <Button aria-label={label} title={title ?? label} className={`menu-trigger ${className}`}>
          {children}
        </Button>
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content className="menu" align={align} side={side} sideOffset={4}>
          {items.map((item, index) =>
            item === "separator" ? (
              <DropdownMenu.Separator key={`separator-${index}`} className="menu-separator" />
            ) : (
              <DropdownMenu.Item
                key={item.label}
                className="menu-item"
                title={item.title}
                onSelect={item.onSelect}
              >
                {item.label}
              </DropdownMenu.Item>
            ),
          )}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}
