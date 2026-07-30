export type AccessWindow = {
  days: string[];
  start?: string;
  end?: string;
  all_day: boolean;
};

export type DeviceProfile = {
  id: string;
  name: string;
  enabled: boolean;
  access_mode: "allow_all" | "allow_services";
  allowed_services: string[];
  windows: AccessWindow[];
};

export const weekdays = ["monday", "tuesday", "wednesday", "thursday", "friday"] as const;
export const weekend = ["saturday", "sunday"] as const;

export function idFromName(value: string, suffix: string): string {
  const slug = value
    .toLowerCase()
    .replace(/đ/g, "dj")
    .normalize("NFKD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 30) || "device";
  return `${slug}-${suffix}`.slice(0, 48);
}

export function normalizedHostname(value: string): string {
  return idFromName(value, "host").replace(/-host$/, "");
}

export function macIsValid(value: string): boolean {
  return /^([0-9a-f]{2}:){5}[0-9a-f]{2}$/i.test(value.trim());
}

export function ipv4IsValid(value: string): boolean {
  const parts = value.trim().split(".");
  return parts.length === 4 && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255);
}

export function netmaskFromCIDR(cidr: string): string | null {
  const [address, rawPrefix, extra] = cidr.trim().split("/");
  if (extra !== undefined || !ipv4IsValid(address) || !/^\d{1,2}$/.test(rawPrefix || "")) return null;
  const prefix = Number(rawPrefix);
  if (!Number.isInteger(prefix) || prefix < 1 || prefix > 30) return null;
  const mask = (0xffffffff << (32 - prefix)) >>> 0;
  return [24, 16, 8, 0].map((shift) => String((mask >>> shift) & 0xff)).join(".");
}

export function buildDeviceScheduleProfile(options: {
  id: string;
  name: string;
  accessMode: "allow_all" | "allow_services";
  allowedServices: string[];
  weekdayStart: string;
  weekdayEnd: string;
  weekendAllDay: boolean;
}): DeviceProfile {
  const windows: AccessWindow[] = [
    {
      days: [...weekdays],
      start: options.weekdayStart,
      end: options.weekdayEnd,
      all_day: false,
    },
  ];
  if (options.weekendAllDay) {
    windows.push({ days: [...weekend], all_day: true });
  }
  return {
    id: options.id,
    name: options.name,
    enabled: true,
    access_mode: options.accessMode,
    allowed_services: options.accessMode === "allow_services" ? [...options.allowedServices] : [],
    windows,
  };
}
