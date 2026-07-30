export type AccessWindow = { start: string; end: string };

export type DeviceProfile = {
  id: string;
  name: string;
  ip_addresses: string[];
  services: string[];
  enabled: boolean;
  schedule: {
    weekday_windows: AccessWindow[];
    weekend_mode: "all_day" | "blocked" | "same_as_weekdays" | "custom";
    weekend_windows: AccessWindow[];
  };
};

export const managedServices = [
  ["youtube", "YouTube"],
  ["steam", "Steam"],
  ["tiktok", "TikTok"],
  ["instagram", "Instagram"],
  ["facebook", "Facebook / Messenger"],
  ["roblox", "Roblox"],
  ["epic", "Epic Games"],
  ["twitch", "Twitch"],
] as const;

export function createKidsProfile(input: {
  id?: string;
  name?: string;
  addresses: string[];
  services: string[];
  weekdayStart: string;
  weekdayEnd: string;
  weekendAllDay: boolean;
}): DeviceProfile {
  const addresses = input.addresses.map((address) => address.trim()).filter(Boolean);
  const services = [...new Set(input.services)];
  if (addresses.length === 0) throw new Error("Dodajte najmanje jednu statičku IP adresu uređaja.");
  if (services.length === 0) throw new Error("Odaberite najmanje jedan servis.");
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(input.weekdayStart) || !/^([01]\d|2[0-3]):[0-5]\d$/.test(input.weekdayEnd)) {
    throw new Error("Vrijeme mora biti u HH:MM formatu.");
  }
  if (input.weekdayStart >= input.weekdayEnd) {
    throw new Error("Kraj radnog dana mora biti poslije početka.");
  }
  return {
    id: input.id ?? `kids-${crypto.randomUUID()}`,
    name: input.name?.trim() || "Kids",
    ip_addresses: addresses,
    services,
    enabled: true,
    schedule: {
      weekday_windows: [{ start: input.weekdayStart, end: input.weekdayEnd }],
      weekend_mode: input.weekendAllDay ? "all_day" : "blocked",
      weekend_windows: [],
    },
  };
}

export function describeSchedule(profile: DeviceProfile): string {
  const weekdays = profile.schedule.weekday_windows
    .map((window) => `${window.start}–${window.end}`)
    .join(", ") || "blokirano";
  const weekend = profile.schedule.weekend_mode === "all_day"
    ? "cijeli dan"
    : profile.schedule.weekend_mode === "same_as_weekdays"
      ? weekdays
      : profile.schedule.weekend_mode === "custom"
        ? profile.schedule.weekend_windows.map((window) => `${window.start}–${window.end}`).join(", ")
        : "blokirano";
  return `Pon–pet ${weekdays}; vikend ${weekend}`;
}
