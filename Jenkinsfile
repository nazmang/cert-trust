pipeline {

    agent { label 'docker' }

    environment {
        BASE_VERSION = '0.1.0'
    }

    stages {
        stage('docker build and push') {
            steps {
                script {
                    sh '''
                      commit=`git log -1 --pretty=format:%h`
                      docker build --push -t harbor.flolive.net/flo-bss/cert-trust:${BASE_VERSION} -t harbor.flolive.net/flo-bss/cert-trust:latest .
                      docker rmi harbor.flolive.net/flo-bss/cert-trust:${BASE_VERSION} harbor.flolive.net/flo-bss/cert-trust:latest
                    '''
                }
            }
        }
    }
}
