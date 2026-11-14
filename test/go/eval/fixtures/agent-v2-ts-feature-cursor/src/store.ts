import type { Note } from "./types.ts";

export class NoteStore {
  private notes: Note[] = [];

  add(n: Note): void {
    this.notes.push(n);
  }

  // all returns the notes in insertion order.
  all(): Note[] {
    return [...this.notes];
  }
}
