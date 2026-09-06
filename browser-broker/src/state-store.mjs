import { createCipheriv, createDecipheriv, createHash, createHmac, randomBytes } from 'node:crypto';
import { chmod, mkdir, readFile, rename, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';

const envelopeVersion = 1;

function stateKey(secret, project) {
  return createHmac('sha256', secret)
    .update('remote.futrx/browser-state/v1\n')
    .update(project, 'utf8')
    .digest();
}

function projectDirectory(root, project) {
  const digest = createHash('sha256').update(project).digest('hex');
  return path.join(root, digest);
}

export class EncryptedStateStore {
  constructor(root, secret) {
    this.root = root;
    this.secret = Buffer.from(secret);
    this.operations = new Map();
  }

  directory(project) {
    return projectDirectory(this.root, project);
  }

  async load(project) {
    return this.serialized(project, () => this.loadNow(project));
  }

  async loadNow(project) {
    let envelope;
    try {
      envelope = JSON.parse(await readFile(path.join(this.directory(project), 'storage-state.enc.json'), 'utf8'));
    } catch (error) {
      if (error?.code === 'ENOENT') return undefined;
      throw new Error(`read encrypted browser state: ${error.message}`);
    }
    if (envelope?.version !== envelopeVersion)
      throw new Error('unsupported encrypted browser state version');
    try {
      const nonce = Buffer.from(envelope.nonce, 'base64');
      const tag = Buffer.from(envelope.tag, 'base64');
      const ciphertext = Buffer.from(envelope.ciphertext, 'base64');
      const decipher = createDecipheriv('aes-256-gcm', stateKey(this.secret, project), nonce);
      decipher.setAuthTag(tag);
      const plaintext = Buffer.concat([decipher.update(ciphertext), decipher.final()]);
      return JSON.parse(plaintext.toString('utf8'));
    } catch {
      throw new Error('decrypt browser state: authentication failed');
    }
  }

  async save(project, state) {
    return this.serialized(project, () => this.saveNow(project, state));
  }

  async saveNow(project, state) {
    const directory = this.directory(project);
    await mkdir(directory, { recursive: true, mode: 0o700 });
    await chmod(directory, 0o700);
    const nonce = randomBytes(12);
    const cipher = createCipheriv('aes-256-gcm', stateKey(this.secret, project), nonce);
    const plaintext = Buffer.from(JSON.stringify(state), 'utf8');
    const ciphertext = Buffer.concat([cipher.update(plaintext), cipher.final()]);
    const envelope = {
      version: envelopeVersion,
      nonce: nonce.toString('base64'),
      tag: cipher.getAuthTag().toString('base64'),
      ciphertext: ciphertext.toString('base64'),
    };
    const target = path.join(directory, 'storage-state.enc.json');
    const temporary = `${target}.${process.pid}.${randomBytes(6).toString('hex')}.tmp`;
    try {
      await writeFile(temporary, `${JSON.stringify(envelope)}\n`, { mode: 0o600 });
      await rename(temporary, target);
      await chmod(target, 0o600);
    } finally {
      await rm(temporary, { force: true }).catch(() => {});
    }
  }

  async delete(project) {
    await this.serialized(project, () => rm(this.directory(project), { recursive: true, force: true }));
  }

  async serialized(project, operation) {
    const previous = this.operations.get(project) || Promise.resolve();
    const current = previous.catch(() => {}).then(operation);
    this.operations.set(project, current);
    try {
      return await current;
    } finally {
      if (this.operations.get(project) === current) this.operations.delete(project);
    }
  }
}
