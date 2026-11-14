import type { Note } from "./types.ts";

export interface Position {
  createdAt: string;
  id: string;
}

function cmp(a: Position, b: Position): number {
  if (a.createdAt !== b.createdAt) return a.createdAt < b.createdAt ? -1 : 1;
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
}

export class NoteStore {
  private notes: Note[] = [];

  add(n: Note): void {
    this.notes.push(n);
  }

  // all returns the notes in insertion order.
  all(): Note[] {
    return [...this.notes];
  }

  // page returns up to limit notes strictly after `after`, sorted, plus whether more exist.
  page(after: Position | null, limit: number): { items: Note[]; more: boolean } {
    const sorted = [...this.notes].sort(cmp);
    const rest = after ? sorted.filter((n) => cmp(n, after) > 0) : sorted;
    return { items: rest.slice(0, limit), more: rest.length > limit };
  }
}
