import { Fragment, type ReactNode } from "react";

/**
 * Lets a long single-word value (hostname, SSID, host:port) wrap after its
 * separators instead of mid-word. Large hero values otherwise broke as
 * "router.example.c" / "om" or "MinimalRouter-" / "Home" at laptop widths.
 */
export function breakable(value: string): ReactNode {
  const parts = value.split(/(?<=[.\-_:])/);
  if (parts.length < 2) return value;
  return parts.map((part, index) => (
    <Fragment key={index}>
      {part}
      {index < parts.length - 1 && <wbr />}
    </Fragment>
  ));
}
