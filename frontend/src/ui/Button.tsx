import type { ButtonHTMLAttributes, ReactNode } from "react";
export function Button({
  children,
  className = "",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement>) {
  return (
    <button type="button" className={`button ${className}`} {...props}>
      {children}
    </button>
  );
}
export function IconButton({
  label,
  children,
  ...props
}: Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> & {
  label: string;
  children: ReactNode;
}) {
  return (
    <Button
      aria-label={label}
      title={label}
      {...props}
      className={`icon-button ${props.className ?? ""}`}
    >
      {children}
    </Button>
  );
}
