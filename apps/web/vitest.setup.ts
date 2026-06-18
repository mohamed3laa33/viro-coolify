import "@testing-library/jest-dom/vitest";

// jsdom under vitest does not always back `window.localStorage` with a real
// Storage implementation (it can surface as an empty object whose methods are
// undefined), which breaks code and tests that call getItem/setItem/clear.
// Install a spec-compliant in-memory Storage so every test gets working
// `localStorage`/`sessionStorage`.
class MemoryStorage implements Storage {
  private store = new Map<string, string>();

  get length(): number {
    return this.store.size;
  }

  clear(): void {
    this.store.clear();
  }

  getItem(key: string): string | null {
    return this.store.has(key) ? this.store.get(key)! : null;
  }

  key(index: number): string | null {
    return Array.from(this.store.keys())[index] ?? null;
  }

  removeItem(key: string): void {
    this.store.delete(key);
  }

  setItem(key: string, value: string): void {
    this.store.set(key, String(value));
  }
}

function ensureStorage(name: "localStorage" | "sessionStorage"): void {
  const current = (globalThis as unknown as Record<string, unknown>)[name];
  const usable =
    current != null && typeof (current as Storage).clear === "function";
  if (usable) return;
  Object.defineProperty(globalThis, name, {
    value: new MemoryStorage(),
    configurable: true,
    writable: true,
  });
}

ensureStorage("localStorage");
ensureStorage("sessionStorage");
