import type { Readable, Writable } from 'node:stream';
import type { Frame } from './frames.js';
export type Received = {
    t: string;
    [member: string]: unknown;
};
export declare class Channel {
    #private;
    constructor(input: Readable, output: Writable);
    recv(): Promise<Received | null>;
    send(frame: Frame): Promise<void>;
    write(line: string): Promise<void>;
}
