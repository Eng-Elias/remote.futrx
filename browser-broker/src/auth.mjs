import { createHmac, timingSafeEqual } from 'node:crypto';

const projectTokenVersion = 'v1';
const controlTokenVersion = 'v1c';
const projectPattern = /^[a-z0-9][a-z0-9-]{0,62}$/;

function encode(value) {
  return Buffer.from(value, 'utf8').toString('base64url');
}

function signature(secret, version, project) {
  return createHmac('sha256', secret)
    .update(`${version}\n${project}`, 'utf8')
    .digest();
}

export function validProjectKey(project) {
  return projectPattern.test(project);
}

function issueScopedToken(secret, version, project) {
  if (!Buffer.isBuffer(secret) || secret.length < 32)
    throw new Error('browser broker secret must contain at least 32 bytes');
  if (!validProjectKey(project))
    throw new Error('invalid browser project key');
  return `${version}.${encode(project)}.${signature(secret, version, project).toString('base64url')}`;
}

function verifyScopedToken(secret, version, token) {
  if (!Buffer.isBuffer(secret) || secret.length < 32 || typeof token !== 'string' || token.length > 512)
    return null;
  const parts = token.split('.');
  if (parts.length !== 3 || parts[0] !== version)
    return null;
  let project;
  let supplied;
  try {
    project = Buffer.from(parts[1], 'base64url').toString('utf8');
    supplied = Buffer.from(parts[2], 'base64url');
  } catch {
    return null;
  }
  if (!validProjectKey(project) || encode(project) !== parts[1])
    return null;
  const expected = signature(secret, version, project);
  if (supplied.length !== expected.length || !timingSafeEqual(supplied, expected))
    return null;
  return project;
}

function bearerFromRequest(request) {
  const authorization = request.headers.authorization || '';
  if (!authorization.startsWith('Bearer '))
    return null;
  return authorization.slice('Bearer '.length).trim();
}

export function issueProjectToken(secret, project) {
  return issueScopedToken(secret, projectTokenVersion, project);
}

export function issueControlToken(secret, project) {
  return issueScopedToken(secret, controlTokenVersion, project);
}

export function verifyProjectToken(secret, token) {
  return verifyScopedToken(secret, projectTokenVersion, token);
}

export function verifyControlToken(secret, token) {
  return verifyScopedToken(secret, controlTokenVersion, token);
}

export function projectFromRequest(secret, request) {
  return verifyProjectToken(secret, bearerFromRequest(request));
}

export function controlProjectFromRequest(secret, request) {
  return verifyControlToken(secret, bearerFromRequest(request));
}
