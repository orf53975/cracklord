cracklord.controller('JobsController', ['$scope', 'JobsService', 'QueueService', 'growl', 'ResourceList', '$interval', function JobsController($scope, JobsService, QueueService, growl, ResourceList, $interval) {
	var timer
	$scope.listreordered = false;
	$scope.currentjobs = [];
	$scope.completedjobs = [];
	ResourceList.load();

	$scope.sortableOptions = {
		handle: '.draghandle',
		axis: 'y',
		stop: function(e, ui) {
			$scope.listreordered = true;
		}
	};

	$scope.loadJobs = function() {
		JobsService.query(
			//Our success handler
			function(data) {
				$scope.listreordered = false;

				for(var i = 0; i < data.length; i++) {
					if(data[i].resourceid) {
						var id = data[i].resourceid;
						var resource = ResourceList.get(id);
						if(resource) {
							data[i].resourcecolor = "background-color: rgb("+resource.color.r+","+resource.color.g+","+resource.color.b+");";
						}
					}
					
					if(data[i].status == "quit" || data[i].status == "failed" || data[i].status == "done") {
						// First let's check and see if the job is running in our array of running jobs.  if so, we need to delete it.
						cur_idx = $scope.currentjobs.map(function(e) { return e.id; }).indexOf(data[i].id);
						if(cur_idx >= 0) {
							$scope.currentjobs.splice(cur_idx, 1)
						}

						// Now let's see if it's in our array of completed jobs.  If it is, then let's just update the data so we don't get odd refreshes for the user
						cmplt_idx = $scope.completedjobs.map(function(e) { return e.id; }).indexOf(data[i].id);
						if(cmplt_idx >= 0) {
							data[i].expanded = $scope.completedjobs[cmplt_idx].expanded
							$scope.completedjobs[cmplt_idx] = data[i]
						// Otherwise, let's add it to the beginning of our array.
						} else {
							data[i].expanded = false
							$scope.completedjobs.unshift(data[i]);
						}
					} else {
						idx = $scope.currentjobs.map(function(e) { return e.id; }).indexOf(data[i].id);
						if(idx >= 0) {
							data[i].expanded = $scope.currentjobs[idx].expanded
							$scope.currentjobs[idx] = data[i]
						} else {
							data[i].expanded = false;
							$scope.currentjobs.push(data[i]);
						}
					}
				}

			},
			//Our error handler
			function(error) {
				growl.error(error.data.message)
			}
		);
	}

	//Setup a timer to refresh the data on a regular basis.
	timer = $interval(function() {
		if (!$scope.listreordered) {
			$scope.loadJobs();
		}
	}, 15000);

	$scope.$on('$destroy', function() {
		$interval.cancel(timer)
	});

	//Initially we'll also load our data	
	$scope.loadJobs();
}]);

cracklord.directive('jobReorderConfirm', ['QueueService', 'growl', function jobReorderConfirm(QueueService, growl) {
	return {
		restrict: 'E',
		templateUrl: 'components/Jobs/jobsReorderConfirm.html', 
		replace: true,
		scope: {
			reload: "&",
			current: "=",
			complete: "=",
			dragstatus: "="
		},
		controller: function($scope) {
			$scope.reorderConfirm = function() {
				var cur_ids = $scope.current.map(function (job) {
					if(job) {
						return job.id;
					}
				});
				var done_ids = $scope.complete.map(function (job) {
					if(job) {
						return job.id;
					}
				});

				total = cur_ids.concat(done_ids)

				QueueService.reorder(total)
					.success(function(data, status, headers, config) {
						growl.success("Job data reordered successfully.");
						$scope.dragstatus = false;
						$scope.reload();
					})
					.error(function(data, status, headers, config) {
						growl.error(data.message);
						$scope.dragstatus = false;
					});
			}

			$scope.reorderCancel = function() {
				$scope.dragstatus = false;
				$scope.reload();
				growl.info("Reordering of jobs cancelled.")
			}
		}
	}
}]);

cracklord.directive('jobDetail', ['JobsService', 'ToolsService', 'growl', 'ResourceList', '$interval', '$q', function jobDetail(JobsService, ToolsService, growl, ResourceList, $interval, $q) {
	return {
		restrict: 'E',
		templateUrl: 'components/Jobs/jobsViewDetail.html',
		scope: {
			jobid: '@',
			visibility: '='
		},
		controller: function($scope) {
			// Mmmmmmmm.... Donut.
			$scope.processDonut = function(animate) {
				$scope.donut = {};
				$scope.donut.labels = ['Processed', 'Remaining'];

				var total = 100 - $scope.detail.progress;
				$scope.donut.data = [$scope.detail.progress, total];
				$scope.donut.colors = [ '#337ab7', '#aaaaaa' ];
				$scope.donut.options = {
					'animateRotate': animate,
					'animation': animate
				}
			};

			$scope.processLine = function(animate) {
				$scope.line = {};
				$scope.line.series = [ $scope.detail.performancetitle ]; 
				$scope.line.data = [];
				$scope.line.data[0] = [];
				$scope.line.labels = [];
				$scope.line.options = {
					'pointDot': false,
					'showTooltips': false,
					'animation': animate
				};
				$scope.line.colors = [
					'#d43f3a'
				]

				var sorted_times = Object.keys($scope.detail.performancedata).sort()
				var len = sorted_times.length - 1
				var min = Math.max(0, len-240)
				var step = len > 60 ? 3 : 1
				for (var i = min; i <= len; i=i+step) {
					time = sorted_times[i]
					$scope.line.data[0].push($scope.detail.performancedata[time]);
					if((i - min) % 20 == 0) {
						var date = new Date(time * 1000)
						var m = ('0'+date.getMinutes()).slice(-2);
						var h = date.getHours();
						var ampm = h > 12 ? "PM" : "AM";
						h = h > 12 ? h - 12 : h;
						$scope.line.labels.push(h + ":" + m + " " + ampm);
					} else {
						$scope.line.labels.push("");
					}
				};
			}
		},
		link: function($scope, $element, $attrs) {
			var timer

			$scope.loadData = function(animate) {
				return $q(function(resolve, reject) {
					JobsService.get({id: $scope.jobid}, 
						function success(data) {
							$scope.detail = data.job;
							$scope.processDonut(animate);
							$scope.processLine(animate);
							resolve();
						},
						function error(error) {
							growl.error(error.data.message);
							$($element).find('div.slider').slideUp("slow", function() {
								$element.parent().hide();
							});
							reject();
						}
					);	
				})
			}

			$scope.$watch('visibility', function(newval, oldval) {
				if(newval === true) {
					$scope.loadData(true).then(function() {
						if($scope.detail) {
							var resource = ResourceList.get($scope.detail.resourceid);
							if(resource) {
								$scope.resource = {}
								$scope.resource.name = resource.name;
							}

							ToolsService.get({id: $scope.detail.toolid}, 
								function toolsuccess(data) {
									$scope.tool = data.tool;
								}
							);
						}

						$element.parent().show();
						$element.find('.slider').slideDown();

						if(!timer) {
							timer = $interval(function() {
								//Disable the reload animation at this point
								$scope.loadData(false);
							}, 15000);
						}
					})
				} else {
					$interval.cancel(timer)
					timer = null
					$($element).find('div.slider').slideUp("slow", function() {
						$element.parent().hide();
					});
				}
			});

			$scope.$on('$destroy', function() {
				$interval.cancel(timer)
			});
		},
	}
}]);

cracklord.controller('CreateJobController', ['$scope', '$state', 'ToolsService', 'JobsService', 'growl', function CreateJobController($scope, $state, ToolsService, JobsService, growl) {
	$scope.formData = {};
	$scope.formData.params = {};

	$scope.toolChange = function() {
		var id = $scope.formData.tool.id;
		var tool = ToolsService.get({id: id}, 
			function(data) {
				$scope.tool = data.tool;
			}, 
			function(error) {
				growl.error(error.data.message);
			}
		);
	}

	$scope.processNewJobForm = function() {
		var newjob = new JobsService();

		newjob.toolid = $scope.formData.tool.id;
		newjob.name = $scope.formData.name;
		newjob.params = $scope.formData.params;
		
		JobsService.save(newjob, 
			function(data) {
				growl.success("Job successfully added");
				$state.transitionTo('jobs');
			}, 
			function(error) {
				growl.error(error.data.message);
			}
		);
	}	
}]);