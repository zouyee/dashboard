// Copyright 2015 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/**
 * @fileoverview Common configuration constants used in other build/test files.
 */
import gulpUtil from 'gulp-util';
import path from 'path';

/**
 * Load the i18n and l10n configuration. Used when dashboard is built in production.
 */
let localization = require('../i18n/locale_conf.json');

/**
 * Base path for all other paths.
 */
const basePath = path.join(__dirname, '../');

/**
 * Compilation architecture configuration.
 */
const arch = {
  /**
   * Default architecture that the project is compiled to. Used for local development and testing.
   * TODO(bryk): Dynamically determine this based on current arch.
   */
  default: 'amd64',
  /**
   * List of all supported architectures by this project.
   */
  list: ['amd64', 'arm', 'arm64', 'ppc64le', 's390x'],
};

/**
 * Configuration for container registry to push images to.
 */
const containerRegistry = {
  release: 'gcr.io/google_containers',
  /** Default to an environment variable */
  head: process.env.DOCKER_HUB_PREFIX || 'kubernetes',
};

/**
 * Package version information.
 */
const version = {
  /**
   * Current release version of the project.
   */
  release: 'v1.6.0',
  /**
   * Version name of the head release of the project.
   */
  head: 'head',
};

/**
 * Base name for the docker image.
 */
const imageNameBase = 'kubernetes-dashboard';

/**
 * Exported configuration object with common constants used in build pipeline.
 */
export default {
  /**
   * Configuration for container registry to push images to.
   */
  containerRegistry: containerRegistry,

  /**
   * Backend application constants.
   */
  backend: {
    /**
     * The name of the backend binary.
     */
    binaryName: 'dashboard',
    /**
     * Name of the main backend package that is used in go build command.
     */
    mainPackageName: 'gerrit.cmss.com/BC-PaaS/backend/src/app/backend',
    /**
     * Port number of the backend server. Only used during development.
     */
    devServerPort: 9091,
    /**
     * Address for the Kubernetes API server.
     */
    apiServerHost: 'http://localhost:8080',
    /**
     * Env variable with address for the Kubernetes API server.
     */
    envApiServerHost: process.env.KUBE_DASHBOARD_APISERVER_HOST,
    /**
     * Env variable with path to kubeconfig file.
     */
    envKubeconfig: process.env.KUBE_DASHBOARD_KUBECONFIG,
    /**
     * Address for the Heapster API server. If blank, the dashboard
     * will attempt to connect to Heapster via a service proxy.
     */
    heapsterServerHost:
        gulpUtil.env.heapsterServerHost !== undefined ? gulpUtil.env.heapsterServerHost : '',
    /**
     * Variables used to differentiate between prod and dev build.
     */
    production: 'prod',
    development: 'dev',
  },

  /**
   * Project compilation architecture info.
   */
  arch: arch,

  /**
   * Deployment constants configuration.
   */
  deploy: {
    /**
     * Project version info.
     */
    version: version,

    /**
     * Image name base for current architecture.
     */
    imageNameBase: `${imageNameBase}-${arch.default}`,

    /**
     * Image name for the head release for current architecture.
     */
    headImageName: `${containerRegistry.head}/${imageNameBase}-${arch.default}:${version.head}`,

    /**
     * Image name for the versioned release for current architecture.
     */
    releaseImageName:
        `${containerRegistry.release}/${imageNameBase}-${arch.default}:${version.release}`,

    /**
     * Image name for the head release for all supported architecture.
     */
    headImageNames: arch.list.map(
        (arch) => `${containerRegistry.head}/${imageNameBase}-${arch}:${version.head}`),

    /**
     * Image name for the versioned release for all supported architecture.
     */
    releaseImageNames: arch.list.map(
        (arch) => `${containerRegistry.release}/${imageNameBase}-${arch}:${version.release}`),
  },

  /**
   * Frontend application constants.
   */
  frontend: {
    /**
     * Port number to access the dashboard UI
     */
    serverPort: 9090,
    /**
     * The name of the root Angular module, i.e., the module that bootstraps the application.
     */
    rootModuleName: 'kubernetesDashboard',
  },

  /**
   * Configuration for tests.
   */
  test: {
    /**
     * Whether to use sauce labs for running tests that require a browser.
     */
    useSauceLabs: !!process.env.TRAVIS,
  },

  /**
   * Configuration for i18n & l10n.
   */
  translations: localization.translations.map((translation) => {
    return {path: path.join(basePath, 'i18n', translation.file), key: translation.key};
  }),

  /**
   * Absolute paths to known directories, e.g., to source directory.
   */
  paths: {
    app: path.join(basePath, 'src/app'),
    assets: path.join(basePath, 'src/app/assets'),
    base: basePath,
    backendSrc: path.join(basePath, 'src/app/backend'),
    backendTmp: path.join(basePath, '.tmp/backend'),
    backendTmpSrc:
        path.join(basePath, '.tmp/backend/src/gerrit.cmss.com/BC-PaaS/backend/src/app/backend'),
    backendTmpSrcVendor:
        path.join(basePath, '.tmp/backend/src/gerrit.cmss.com/BC-PaaS/backend/vendor'),
    backendVendor: path.join(basePath, 'vendor'),
    bowerComponents: path.join(basePath, 'bower_components'),
    build: path.join(basePath, 'build'),
    coverage: path.join(basePath, 'coverage'),
    coverageBackend: path.join(basePath, 'coverage/go.txt'),
    coverageFrontend: path.join(basePath, 'coverage/lcov/lcov.info'),
    deploySrc: path.join(basePath, 'src/deploy'),
    dist: path.join(basePath, 'dist', arch.default),
    distCross: arch.list.map((arch) => path.join(basePath, 'dist', arch)),
    distPre: path.join(basePath, '.tmp/dist'),
    distPublic: path.join(basePath, 'dist', arch.default, 'public'),
    distPublicCross: arch.list.map((arch) => path.join(basePath, 'dist', arch, 'public')),
    distRoot: path.join(basePath, 'dist'),
    externs: path.join(basePath, 'src/app/externs'),
    frontendSrc: path.join(basePath, 'src/app/frontend'),
    frontendTest: path.join(basePath, 'src/test/frontend'),
    goTools: path.join(basePath, '.tools/go'),
    goWorkspace: path.join(basePath, '.go_workspace'),
    hyperkube: path.join(basePath, 'build/hyperkube.sh'),
    goTestScript: path.join(basePath, 'build/go-test.sh'),
    i18nProd: path.join(basePath, '.tmp/i18n'),
    integrationTest: path.join(basePath, 'src/test/integration'),
    jsoneditorImages: path.join(basePath, 'bower_components/jsoneditor/src/css/img'),
    karmaConf: path.join(basePath, 'build/karma.conf.js'),
    materialIcons: path.join(basePath, 'bower_components/material-design-icons/iconfont'),
    nodeModules: path.join(basePath, 'node_modules'),
    partials: path.join(basePath, '.tmp/partials'),
    messagesForExtraction: path.join(basePath, '.tmp/messages_for_extraction'),
    prodTmp: path.join(basePath, '.tmp/prod'),
    protractorConf: path.join(basePath, 'build/protractor.conf.js'),
    robotoFonts: path.join(basePath, 'bower_components/roboto-fontface/fonts'),
    robotoFontsBase: path.join(basePath, 'bower_components/roboto-fontface'),
    robotoMonoFonts: path.join(basePath, 'bower_components/easyfont-roboto-mono/fonts'),
    robotoMonoFontsBase: path.join(basePath, 'bower_components/easyfont-roboto-mono'),
    serve: path.join(basePath, '.tmp/serve'),
    src: path.join(basePath, 'src'),
    tmp: path.join(basePath, '.tmp'),
    xtbgenerator: path.join(basePath, '.tools/xtbgenerator/bin/XtbGenerator.jar'),
  },
};
