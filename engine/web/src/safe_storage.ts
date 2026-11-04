// safe_storage.ts — M36.27: reaching web storage must not throw.
//
// Every storage helper in this client (resume.ts, preferences.ts, the hint and
// comfort stores) catches errors from its own getItem/setItem calls. But the
// Storage it is handed was read from window.localStorage or
// window.sessionStorage at the call site, and that property access is itself
// what throws when storage is unavailable: a sandboxed frame (the watch-live
// embed), or a browser told to block site data, raises SecurityError from the
// getter. The throw happened outside every guard and took the caller with it.
//
// safeStorage is that guard. When the getter throws it returns an in-memory
// Storage instead, kept for the life of the page, so the client behaves as it
// does with storage that is merely empty: nothing survives a reload, and
// everything else works.

type StorageName = "localStorage" | "sessionStorage";

class MemoryStorage implements Storage {
  private items = new Map<string, string>();

  get length(): number {
    return this.items.size;
  }

  clear(): void {
    this.items.clear();
  }

  getItem(key: string): string | null {
    return this.items.has(key) ? (this.items.get(key) as string) : null;
  }

  key(index: number): string | null {
    return Array.from(this.items.keys())[index] ?? null;
  }

  removeItem(key: string): void {
    this.items.delete(key);
  }

  setItem(key: string, value: string): void {
    this.items.set(key, String(value));
  }
}

const fallbacks = new Map<StorageName, Storage>();

/**
 * safeStorage returns window[name], or a page-lifetime in-memory Storage if
 * reading that property throws or yields nothing. `win` is injectable for tests.
 */
export function safeStorage(name: StorageName, win: unknown = globalThis.window): Storage {
  try {
    const storage = (win as Record<StorageName, Storage | undefined>)[name];
    if (storage) {
      return storage;
    }
  } catch {
    // fall through to the in-memory store
  }
  let fallback = fallbacks.get(name);
  if (!fallback) {
    fallback = new MemoryStorage();
    fallbacks.set(name, fallback);
  }
  return fallback;
}
