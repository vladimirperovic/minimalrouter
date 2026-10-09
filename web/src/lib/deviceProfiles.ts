export type AccessWindow = { start: string; end: string };

export const scheduleDays = [
  ["monday", "Mon"],
  ["tuesday", "Tue"],
  ["wednesday", "Wed"],
  ["thursday", "Thu"],
  ["friday", "Fri"],
  ["saturday", "Sat"],
  ["sunday", "Sun"],
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
  ["adult", "Adult service group"],
  ["gaming", "Gaming service group"],
] as const;

const emptyWindows = (): DayWindows => Object.fromEntries(
  scheduleDays.map(([day]) => [day, [] as AccessWindow[]]),
) as unknown as DayWindows;

export function createDefaultKidsGrid(): HourGrid {
  return Object.fromEntries(scheduleDays.map(([day], dayIndex) => [
    day,
    Array.from({ length: 24 }, (_, hour) => dayIndex < 5 ? hour >= 19 : true),
  ])) as unknown as HourGrid;
}

export function createEmptyGrid(): HourGrid {
  return Object.fromEntries(scheduleDays.map(([day]) => [day, Array<boolean>(24).fill(false)])) as unknown as HourGrid;
}

export function slotsToWindows(slots: boolean[]): AccessWindow[] {
  if (slots.length !== 24) throw new Error("The scheduler must contain 24 hours for every day.");
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
  return Object.fromEntries(scheduleDays.map(([day]) => [day, slotsToWindows(grid[day])])) as unknown as DayWindows;
}

const minutes = (time: string) => Number(time.slice(0,2))*60 + Number(time.slice(3,5));
const endMinutes = (time: string) => time === "23:59" ? 1440 : minutes(time);
const clockTime = (minute: number) => minute === 1440 ? "23:59" : `${String(Math.floor(minute/60)).padStart(2,"0")}:${String(minute%60).padStart(2,"0")}`;

export function hourCoverage(windows: AccessWindow[], hour: number): number {
  return Math.min(1, windows.reduce((sum, window) => sum + Math.max(0, Math.min(endMinutes(window.end), (hour+1)*60)-Math.max(minutes(window.start), hour*60)), 0)/60);
}

// Change exactly one hour. Minute-precise fragments outside that hour, and
// every other day, retain their original boundaries.
export function paintScheduleHour(windows: AccessWindow[], hour: number, allowed: boolean): AccessWindow[] {
  const start = hour*60, end = start+60;
  const intervals: [number, number][] = [];
  for (const window of windows) {
    const from = minutes(window.start), to = endMinutes(window.end);
    if (!Number.isFinite(from) || !Number.isFinite(to) || from >= to) continue;
    if (from < start) intervals.push([from, Math.min(to,start)]);
    if (to > end) intervals.push([Math.max(from,end),to]);
  }
  if (allowed) intervals.push([start,end]);
  intervals.sort((a,b) => a[0]-b[0]);
  const merged: [number,number][] = [];
  for (const interval of intervals) {
    const previous = merged.at(-1);
    if (previous && interval[0] <= previous[1]) previous[1] = Math.max(previous[1], interval[1]);
    else merged.push([...interval]);
  }
  return merged.map(([from,to]) => ({ start:clockTime(from), end:clockTime(to) }));
}

export function normalizeDayWindows(schedule: DeviceProfile["schedule"]): DayWindows {
  if (schedule.day_windows && Object.keys(schedule.day_windows).length > 0) {
    return Object.fromEntries(scheduleDays.map(([day]) => [day, schedule.day_windows?.[day] ?? []])) as unknown as DayWindows;
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
  const addresses = [...new Set(input.addresses.map((address) => address.trim()).filter(Boolean))];
  const services = [...new Set(input.services)];
  if (addresses.length === 0) throw new Error("Add at least one static device IP address.");
  if (addresses.length > 32 || addresses.some(address => !/^(\d{1,3}\.){3}\d{1,3}$/.test(address) || address.split(".").some(part => Number(part) > 255 || String(Number(part)) !== part))) throw new Error("Use up to 32 valid IPv4 addresses, separated by commas or spaces.");
  if (services.length === 0) throw new Error("Select at least one service.");
  for (const [day] of scheduleDays) {
    if (input.dayWindows[day].length > 12) throw new Error("Use at most 12 time windows per day.");
    for (const window of input.dayWindows[day]) {
      if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(window.start) || !/^([01]\d|2[0-3]):[0-5]\d$/.test(window.end)) {
        throw new Error("Time must use the HH:MM format.");
      }
      if (window.start >= window.end) throw new Error("The end of the allowed period must be after its start.");
    }
    const sorted = [...input.dayWindows[day]].sort((a,b) => a.start.localeCompare(b.start));
    if (sorted.some((window,index) => index > 0 && window.start < sorted[index-1].end)) throw new Error("Time windows on the same day must not overlap.");
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
  if (windows.length === 0) return "blocked";
  if (windows.length === 1 && windows[0].start === "00:00" && windows[0].end === "23:59") return "all day";
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
