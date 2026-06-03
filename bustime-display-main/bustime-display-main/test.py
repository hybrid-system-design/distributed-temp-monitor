import requests
import ssl
import json

def get_time(time):
    return time.split('T')[1].split('.')[0]

api_url = 'https://api.entur.io/journey-planner/v3/graphql'

headers = {
    'Content-Type': 'application/json',
    'ET-Client-Name': 'janschop-github'
}

# Corrected GraphQL query with proper escaping
graphql_query = '''
{
  stopPlace(id: "NSR:StopPlace:43666") {
    name
    estimatedCalls(timeRange: 72100, numberOfDepartures: 10) {     
      realtime
      expectedDepartureTime
      destinationDisplay {
        frontText
      }
      serviceJourney {
        journeyPattern {
          directionType
          line {
            id
            name
          }
        }
      }      
    }
  }
}
'''

# Construct the JSON payload with the corrected GraphQL query
payload = {'query': graphql_query}

response = requests.post(api_url, headers=headers, json=payload)

data_berg = response.json()

# Now you can process the JSON data
#print(data_berg['data']['stopPlace']['estimatedCalls'][0]['destinationDisplay']['frontText'])
berg_outbound = []
berg_inbound = []

for call in data_berg['data']['stopPlace']['estimatedCalls']:
    if call['serviceJourney']['journeyPattern']['directionType'] == 'outbound':
        #print(call['destinationDisplay']['frontText'], get_time(call['expectedDepartureTime']))
        berg_outbound.append(call)
    else:
        #print(call['destinationDisplay']['frontText'], get_time(call['expectedDepartureTime']))
        berg_inbound.append(call)

print('Berg outbound:')
for call in berg_outbound:
    print(call['destinationDisplay']['frontText'], get_time(call['expectedDepartureTime']))

print('\nBerg inbound:')
for call in berg_inbound:
    print(call['destinationDisplay']['frontText'], get_time(call['expectedDepartureTime']))