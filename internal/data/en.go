package data

var EnFirstNames = []string{
	"James", "Mary", "John", "Patricia", "Robert", "Jennifer", "Michael", "Linda", "William", "Elizabeth",
	"David", "Barbara", "Richard", "Susan", "Joseph", "Jessica", "Thomas", "Sarah", "Charles", "Karen",
	"Daniel", "Nancy", "Matthew", "Lisa", "Anthony", "Emily", "Mark", "Ashley", "Steven", "Michelle",
	"Emma", "Olivia", "Noah", "Liam", "Ava", "Sophia", "Lucas", "Mia", "Ethan", "Amelia",
}

var EnLastNames = []string{
	"Smith", "Johnson", "Williams", "Brown", "Jones", "Garcia", "Miller", "Davis", "Rodriguez", "Martinez",
	"Hernandez", "Lopez", "Gonzalez", "Wilson", "Anderson", "Thomas", "Taylor", "Moore", "Jackson", "Martin",
	"Lee", "Perez", "Thompson", "White", "Harris", "Clark", "Lewis", "Walker", "Hall", "Young",
	"King", "Wright", "Scott", "Green", "Baker", "Adams", "Nelson", "Hill", "Campbell", "Mitchell",
}

// EnStates: real US states / cities / counties, ZIP prefixes and area codes.
var EnStates = []Province{
	{Name: "California", Abbr: "CA", Cities: []City{
		{"Los Angeles", []string{"Los Angeles County"}, "900", "213", -118.24, 34.05},
		{"San Francisco", []string{"San Francisco County"}, "941", "415", -122.42, 37.77},
		{"San Diego", []string{"San Diego County"}, "921", "619", -117.16, 32.72},
		{"San Jose", []string{"Santa Clara County"}, "951", "408", -121.89, 37.34},
	}},
	{Name: "New York", Abbr: "NY", Cities: []City{
		{"New York", []string{"New York County"}, "100", "212", -74.01, 40.71},
		{"Buffalo", []string{"Erie County"}, "142", "716", -78.88, 42.89},
	}},
	{Name: "Texas", Abbr: "TX", Cities: []City{
		{"Houston", []string{"Harris County"}, "770", "713", -95.37, 29.76},
		{"Austin", []string{"Travis County"}, "787", "512", -97.74, 30.27},
		{"Dallas", []string{"Dallas County"}, "752", "214", -96.80, 32.78},
	}},
	{Name: "Washington", Abbr: "WA", Cities: []City{
		{"Seattle", []string{"King County"}, "981", "206", -122.33, 47.61},
	}},
	{Name: "Illinois", Abbr: "IL", Cities: []City{
		{"Chicago", []string{"Cook County"}, "606", "312", -87.63, 41.88},
	}},
	{Name: "Massachusetts", Abbr: "MA", Cities: []City{
		{"Boston", []string{"Suffolk County"}, "021", "617", -71.06, 42.36},
	}},
	{Name: "Florida", Abbr: "FL", Cities: []City{
		{"Miami", []string{"Miami-Dade County"}, "331", "305", -80.19, 25.76},
		{"Orlando", []string{"Orange County"}, "328", "407", -81.38, 28.54},
	}},
	{Name: "Georgia", Abbr: "GA", Cities: []City{
		{"Atlanta", []string{"Fulton County"}, "303", "404", -84.39, 33.75},
	}},
	{Name: "Colorado", Abbr: "CO", Cities: []City{
		{"Denver", []string{"Denver County"}, "802", "303", -104.99, 39.74},
	}},
}

var EnStreetNames = []string{"Main", "Oak", "Maple", "Cedar", "Elm", "Pine", "Washington", "Lake", "Hill", "Park",
	"Sunset", "Lincoln", "Jefferson", "Highland", "Madison", "Church", "Spring", "River", "Walnut", "Chestnut"}

var EnStreetSuffixes = []string{"St", "Ave", "Rd", "Blvd", "Dr", "Ln", "Way", "Ct"}

var EnEmailDomains = []string{"gmail.com", "yahoo.com", "outlook.com", "hotmail.com", "icloud.com", "example.com"}

var EnCompanyWords = []string{"Global", "United", "Pacific", "Summit", "Bright", "Blue", "Pioneer", "Apex", "Vertex", "Northstar"}

var EnCompanyIndustries = []string{"Technologies", "Systems", "Labs", "Solutions", "Logistics", "Media", "Foods", "Capital", "Health", "Software"}

var EnCompanySuffixes = []string{"Inc.", "LLC", "Corp.", "Group", "Co."}

var EnJobs = []string{"Software Engineer", "Product Manager", "UX Designer", "Sales Manager", "Accountant", "Teacher",
	"Nurse", "Attorney", "Marketing Specialist", "Data Analyst", "HR Coordinator", "Customer Support Rep",
	"QA Engineer", "Project Manager", "Solutions Architect", "Financial Analyst", "Office Manager", "DevOps Engineer"}

var EnCountries = []string{"United States", "China", "Japan", "Germany", "France", "United Kingdom", "Canada",
	"Australia", "Singapore", "South Korea", "Italy", "Spain"}

var EnWords = []string{
	"time", "people", "way", "day", "work", "world", "life", "system", "data", "team", "product", "service",
	"customer", "order", "project", "quality", "result", "plan", "goal", "market", "change", "support",
	"simple", "fast", "secure", "stable", "great", "new", "good", "better", "every", "together", "always",
	"build", "make", "help", "keep", "start", "improve", "deliver", "review", "share", "grow", "learn",
	"the", "and", "with", "for", "from", "into", "about", "after", "before", "today", "really", "often",
}

// DomainWords are used to build fake domains / URLs.
var DomainWords = []string{"cloud", "data", "smart", "tech", "blue", "star", "nova", "pixel", "bright", "swift",
	"zen", "peak", "wave", "spark", "orbit", "lumen", "vivid", "quant", "metro", "echo"}

var UserAgents = []string{
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0",
}
