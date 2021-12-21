#!/bin/bash

# ./helm-update.sh index
#   create new index for the chart

# ./helm-update.sh template
#   process the chart to a template

# ./helm-update.sh delete
#   delete the chart from kubernetes

# ./helm-update.sh install
#   install the chart into kubernetes

# ./helm-update.sh install-tgz
#   install the chart from one of the tgz files present locally into kubernetes

case $1 in
  index)
    pushd charts
    helm package lagoon-builddeploy
    helm repo index .
    popd
    ;;
  template)
    helm template charts/lagoon-builddeploy -f charts/lagoon-builddeploy/values.yaml
    ;;
  delete)
    helm delete -n lagoon-builddeploy lagoon-builddeploy
    ;;
  install)
    helm repo add lagoon-builddeploy https://raw.githubusercontent.com/uselagoon/remote-controller/main/charts
    helm upgrade --install -n lagoon-builddeploy lagoon-builddeploy lagoon-builddeploy/lagoon-builddeploy
    ;;
  install-tgz)
    options=($(ls charts | grep tgz))
    if [ ${#options[@]} -ne 0 ]; then
      select chart in "${options[@]}";
      do
        case $chart in
              "$QUIT")
                echo "Unknown option, exiting."
                break
                ;;
              *)
                break
                ;;
        esac
      done
      if [ "$chart" != "" ]; then
        helm upgrade --install -n lagoon-builddeploy lagoon-builddeploy charts/$chart
      fi
    else
      echo "No chart files, exiting."
    fi
    ;;
  *)
    echo "nothing"
    ;;
esac
