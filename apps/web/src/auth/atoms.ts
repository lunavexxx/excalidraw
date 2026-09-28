import { atom } from "../app-jotai";

import type { User } from "./types";

export const currentUserAtom = atom<User | null>(null);
