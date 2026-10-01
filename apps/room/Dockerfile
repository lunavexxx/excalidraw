FROM node:24-alpine AS build
WORKDIR /app
COPY package.json ./
# 单项目无 lockfile 时退化为 npm install;CI/本地可提交 package-lock.json 后换 npm ci
RUN npm install --omit=dev --ignore-scripts || npm install --ignore-scripts
COPY tsconfig.json ./
COPY src ./src
RUN npm install typescript@5.6.2 --no-save --ignore-scripts && npx tsc

FROM node:24-alpine
WORKDIR /app
ENV NODE_ENV=production
COPY --from=build /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY package.json ./
EXPOSE 3002
HEALTHCHECK --interval=10s --timeout=3s --retries=5 CMD wget -qO- http://127.0.0.1:3002/healthz || exit 1
CMD ["node", "dist/index.js"]
