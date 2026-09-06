import dns from 'node:dns/promises';
import net from 'node:net';

const metadataHosts = new Set([
  'metadata',
  'metadata.google.internal',
  'instance-data',
  'instance-data.ec2.internal',
]);

const blockedAddresses = new net.BlockList();
for (const [network, prefix] of [
  ['0.0.0.0', 8],
  ['10.0.0.0', 8],
  ['100.64.0.0', 10],
  ['127.0.0.0', 8],
  ['169.254.0.0', 16],
  ['172.16.0.0', 12],
  ['192.0.0.0', 24],
  ['192.0.2.0', 24],
  ['192.168.0.0', 16],
  ['198.18.0.0', 15],
  ['198.51.100.0', 24],
  ['203.0.113.0', 24],
  ['224.0.0.0', 4],
  ['240.0.0.0', 4],
]) blockedAddresses.addSubnet(network, prefix, 'ipv4');
for (const [network, prefix] of [
  ['::', 128],
  ['::1', 128],
  ['64:ff9b::', 96],
  ['64:ff9b:1::', 48],
  ['100::', 64],
  ['2001:2::', 48],
  ['2001:db8::', 32],
  ['fc00::', 7],
  ['fe80::', 10],
  ['ff00::', 8],
]) blockedAddresses.addSubnet(network, prefix, 'ipv6');

export function isPrivateAddress(address) {
  const family = net.isIP(address);
  return family !== 0 && blockedAddresses.check(address, family === 6 ? 'ipv6' : 'ipv4');
}

export class ProjectNetworkPolicy {
  constructor(project, options = {}) {
    this.project = project;
    this.lookup = options.lookup || dns.lookup;
    this.cache = new Map();
    this.cacheTTL = options.cacheTTL || 60_000;
  }

  async allows(rawURL) {
    let url;
    try {
      url = new URL(rawURL);
    } catch {
      return false;
    }
    if (!['http:', 'https:', 'ws:', 'wss:'].includes(url.protocol))
      return ['about:', 'data:', 'blob:'].includes(url.protocol);

    let hostname = url.hostname.toLowerCase().replace(/\.$/, '');
    if (hostname.startsWith('[') && hostname.endsWith(']')) hostname = hostname.slice(1, -1);
    if (hostname === `${this.project}.lxd`) return true;
    if (hostname.endsWith('.lxd') || hostname === 'localhost' || hostname.endsWith('.localhost') || metadataHosts.has(hostname))
      return false;
    if (isPrivateAddress(hostname)) return false;

    const now = Date.now();
    const cached = this.cache.get(hostname);
    if (cached && cached.expires > now) return cached.allowed;
    try {
      const answers = await this.lookup(hostname, { all: true, verbatim: true });
      const allowed = answers.length > 0 && answers.every((answer) => !isPrivateAddress(answer.address));
      this.cache.set(hostname, { allowed, expires: now + this.cacheTTL });
      return allowed;
    } catch {
      return false;
    }
  }
}
