export type AccessWindow = { start: string; end: string };

export const scheduleDays = [
  ["monday", "Pon"],
  ["tuesday", "Uto"],
  ["wednesday", "Sri"],
  ["thursday", "Čet"],
  ["friday", "Pet"],
  ["saturday", "Sub"],
  ["sunday", "Ned"],
] as const;

export type ScheduleDay = (typeof scheduleDays)[number][0];
export type DayWindows = Record<ScheduleDay, AccessWindow[]>;
export type HourGrid = Record<ScheduleDay, boolean[]>;

export type DeviceProfile = {
  id: string;
  name: string;
  ip_addresses: string[];
  services: string[];
  enabled: boolean;
  schedule: {
    day_windows?: Partial<DayWindows>;
    weekday_windows?: AccessWindow[];
    weekend_mode?: "all_day" | "blocked" | "same_as_weekdays" | "custom";
    weekend_windows?: AccessWindow[];
  };
};

export const managedServices = [
  ["youtube", "YouTube"],
  ["steam", "Steam"],
  ["wiki", "Wikipedia / Wikimedia"],
  ["tiktok", "TikTok"],
  ["instagram", "Instagram"],
  ["facebook", "Facebook / Messenger"],
  ["roblox", "Roblox"],
  ["epic", "Epic Games"],
  ["twitch", "Twitch"],
] as const;

const emptyWindows = (): DayWindows => Object.fromEntries(
  scheduleDays.map(([day]) => [day, []]),
) as DayWindows;

export function createDefaultKidsGrid(): HourGrid {
  return Object.fromEntries(scheduleDays.map(([day], dayIndex) => [
    day,
    Array.from({ length: 24 }, (_, hour) => dayIndex < 5 ? hour >= 19 : true),
  ])) as HourGrid;
}

export function createEmptyGrid(): HourGrid {
  return Object.fromEntries(scheduleDays.map(([day]) => [day, Array(24).fill(false)])) as HourGrid;
}

export function slotsToWindows(slots: boolean[]): AccessWindow[] {
  if (slots.length !== 24) throw new Error("Scheduler mora sadržati 24 sata za svaki dan.");
  const windows: AccessWindow[] = [];
  let start: number | null = null;
  for (let hour = 0; hour <= 24; hour += 1) {
    const allowed = hour < 24 && slots[hour];
    if (allowed && start === null) start = hour;
    if (!allowed && start !== null) {
      windows.push({
        start: `${String(start).padStart(2, "0")}:00`,
        end: hour === 24 ? "23:59" : `${String(hour).padStart(2, "0")}:00`,
      });
      start = null;
    }
  }
  return windows;
}

export function gridToDayWindows(grid: HourGrid): DayWindows {
  return Object.fromEntries(scheduleDays.map(([day]) => [day, slotsToWindows(grid[day])])) as DayWindows;
}

function normalizeDayWindows(schedule: DeviceProfile["schedule"]): DayWindows {
  if (schedule.day_windows && Object.keys(schedule.day_windows).length > 0) {
    return Object.fromEntries(scheduleDays.map(([day]) => [day, schedule.day_windows?.[day] ?? []])) as DayWindows;
  }
  const result = emptyWindows();
  for (const [day] of scheduleDays.slice(0, 5)) result[day] = schedule.weekday_windows ?? [];
  const weekend = schedule.weekend_mode === "all_day"
    ? [{ start: "00:00", end: "23:59" }]
    : schedule.weekend_mode === "same_as_weekdays"
      ? schedule.weekday_windows ?? []
      : schedule.weekend_mode === "custom"
        ? schedule.weekend_windows ?? []
        : [];
  result.saturday = weekend;
  result.sunday = weekend;
  return result;
}

export function createKidsProfile(input: {
  id?: string;
  name?: string;
  addresses: string[];
  services: string[];
  dayWindows: DayWindows;
}): DeviceProfile {
  const addresses = input.addresses.map((address) => address.trim()).filter(Boolean);
  const services = [...new Set(input.services)];
  if (addresses.length === 0) throw new Error("Dodajte najmanje jednu statičku IP adresu uređaja.");
  if (services.length === 0) throw new Error("Odaberite najmanje jedan servis.");
  for (const [day] of scheduleDays) {
    for (const window of input.dayWindows[day]) {
      if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(window.start) || !/^([01]\d|2[0-3]):[0-5]\d$/.test(window.end)) {
        throw new Error("Vrijeme mora biti u HH:MM formatu.");
      }
      if (window.start >= window.end) throw new Error("Kraj dozvoljenog perioda mora biti poslije početka.");
    }
  }
  return {
    id: input.id ?? `kids-${crypto.randomUUID()}`,
    name: input.name?.trim() || "Kids",
    ip_addresses: addresses,
    services,
    enabled: true,
    schedule: { day_windows: input.dayWindows },
  };
}

function describeWindows(windows: AccessWindow[]): string {
  if (windows.length === 0) return "blokirano";
  if (windows.length === 1 && windows[0].start === "00:00" && windows[0].end === "23:59") return "cijeli dan";
  return windows.map((window) => `${window.start}–${window.end}`).join(", ");
}

export function describeSchedule(profile: DeviceProfile): string {
  const windows = normalizeDayWindows(profile.schedule);
  const groups: { start: number; end: number; description: string }[] = [];
  scheduleDays.forEach(([day], index) => {
    const description = describeWindows(windows[day]);
    const previous = groups.at(-1);
    if (previous?.description === description) previous.end = index;
    else groups.push({ start: index, end: index, description });
  });
  return groups.map((group) => {
    const first = scheduleDays[group.start][1];
    const last = scheduleDays[group.end][1];
    return `${group.start === group.end ? first : `${first}–${last}`} ${group.description}`;
  }).join("; ");
}
